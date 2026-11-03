package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const krogerProviderID = "kroger"

// KrogerAppHandler serves /api/households/{id}/kroger-app routes.
type KrogerAppHandler struct {
	DB     *sql.DB
	Vault  *appcred.Vault
	Auth   *auth.Service
	Env    ProviderEnv
	Site   app.RuntimeConfig
	Kroger *kroger.Adapter
	// AfterCredentialSaveTestHook runs after Vault.SaveTx inside saveApp; tests only.
	AfterCredentialSaveTestHook func() error
}

type krogerHouseholdParams struct {
	ID string `json:"id"`
}

type krogerAppPutBody struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Modality     string `json:"modality"`
}

type krogerAppTestBody struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// KrogerApp is the GET shape for a household store app.
type KrogerApp struct {
	Configured    bool    `json:"configured"`
	ClientIDLast4 *string `json:"clientIdLast4"`
	RedirectURI   *string `json:"redirectUri"`
	Modality      *string `json:"modality"`
	ProductAccess string  `json:"productAccess"`
	Status        string  `json:"status"`
	CanManage     bool    `json:"canManage"`
}

// KrogerAppDisabled is returned when DISABLE_KROGER is set.
type KrogerAppDisabled struct {
	Status string `json:"status"`
}

// KrogerAppTest is the test result payload.
type KrogerAppTest struct {
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
}

// KrogerAppSaved is the PUT response.
type KrogerAppSaved struct {
	KrogerApp
	Test KrogerAppTest `json:"test"`
}

func (h *KrogerAppHandler) serverRedirectURI() string {
	if strings.TrimSpace(h.Env.RedirectURI) != "" {
		return strings.TrimSpace(h.Env.RedirectURI)
	}
	if h.Site.AppBaseURL != "" {
		return h.Site.AbsoluteURL("/api/providers/kroger/callback")
	}
	return ""
}

func (h *KrogerAppHandler) requireMember(ctx context.Context, householdID string) (tenant.Scope, auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.Kind != "account" {
		return tenant.Scope{}, auth.Principal{}, Unauthorized()
	}
	if h.Auth != nil {
		role, _, memberOK, err := h.Auth.Membership(ctx, p.UserID, householdID)
		if err != nil {
			return tenant.Scope{}, auth.Principal{}, InternalError(err)
		}
		if !memberOK {
			return tenant.Scope{}, auth.Principal{}, NotFound(auth.SentenceNotFound)
		}
		p.Role = role
	}
	scope, err := tenant.New(householdID)
	if err != nil {
		return tenant.Scope{}, auth.Principal{}, NotFound(auth.SentenceNotFound)
	}
	return scope, p, nil
}

func (h *KrogerAppHandler) requireOwner(ctx context.Context, householdID string) (tenant.Scope, auth.Principal, error) {
	scope, p, err := h.requireMember(ctx, householdID)
	if err != nil {
		return tenant.Scope{}, auth.Principal{}, err
	}
	if p.Role != "owner" {
		return tenant.Scope{}, auth.Principal{}, &HTTPError{
			Code: http.StatusForbidden, Message: auth.SentenceForbidden, ErrorCode: "forbidden_role",
		}
	}
	return scope, p, nil
}

func productAccessString(v *int) string {
	if v == nil {
		return "unknown"
	}
	if *v == 1 {
		return "true"
	}
	return "false"
}

func (h *KrogerAppHandler) buildView(ctx context.Context, scope tenant.Scope, canManage bool) (KrogerApp, error) {
	view := KrogerApp{
		Configured:    false,
		ProductAccess: "unknown",
		Status:        "not_set_up",
		CanManage:     canManage,
	}
	saved, ok, err := h.Vault.Load(ctx, scope, krogerProviderID)
	if err != nil {
		return KrogerApp{}, InternalError(err)
	}
	if !ok {
		if uri := h.serverRedirectURI(); uri != "" {
			view.RedirectURI = &uri
		}
		return view, nil
	}
	view.Configured = true
	last4 := saved.ClientID
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	view.ClientIDLast4 = &last4
	redirect := saved.RedirectURI
	if redirect == "" {
		redirect = h.serverRedirectURI()
	}
	if redirect != "" {
		view.RedirectURI = &redirect
	}
	mod := saved.Modality
	if mod == "" {
		mod = "PICKUP"
	}
	view.Modality = &mod
	view.ProductAccess = productAccessString(saved.ProductAccess)
	view.Status, err = h.deriveStatus(ctx, scope)
	if err != nil {
		return KrogerApp{}, InternalError(err)
	}
	return view, nil
}

func (h *KrogerAppHandler) deriveStatus(ctx context.Context, scope tenant.Scope) (string, error) {
	saved, ok, err := h.Vault.Load(ctx, scope, krogerProviderID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "not_set_up", nil
	}
	appKey := appcred.AppKey(saved.ClientID)
	var connected, reauth, stale int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN state = 'connected' AND (app_key IS NULL OR app_key = ?) THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN state = 'reauth_required' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN state = 'connected' AND app_key IS NOT NULL AND app_key != ? THEN 1 ELSE 0 END), 0)
		FROM provider_connections
		WHERE household_id = ? AND provider_id = 'kroger'`, appKey, appKey, scope.HouseholdID()).Scan(&connected, &reauth, &stale); err != nil {
		return "", err
	}
	if connected > 0 {
		return "connected", nil
	}
	if reauth > 0 || stale > 0 {
		return "needs_reconnect", nil
	}
	return "ready_to_connect", nil
}

// Get handles GET /api/households/{id}/kroger-app.
func (h *KrogerAppHandler) Get(req Request[struct{}, krogerHouseholdParams]) (any, error) {
	scope, p, err := h.requireMember(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, err
	}
	if h.Env.Disabled {
		return KrogerAppDisabled{Status: "disabled"}, nil
	}
	return h.buildView(req.Context, scope, p.Role == "owner")
}

func (h *KrogerAppHandler) blockKrogerTest(householdID string) error {
	if h.Auth == nil || householdID == "" {
		return nil
	}
	if err := h.Auth.BlockKrogerAppTest(householdID); err != nil {
		var ae *auth.Error
		if errors.As(err, &ae) && ae.Code == auth.CodeRateLimited {
			return RateLimited(ae.Retry)
		}
		return InternalError(err)
	}
	return nil
}

func (h *KrogerAppHandler) noteKrogerTest(householdID string) error {
	if h.Auth == nil || householdID == "" {
		return nil
	}
	if err := h.Auth.NoteKrogerAppTest(householdID); err != nil {
		var ae *auth.Error
		if errors.As(err, &ae) && ae.Code == auth.CodeRateLimited {
			return RateLimited(ae.Retry)
		}
		return InternalError(err)
	}
	return nil
}

func (h *KrogerAppHandler) runTest(ctx context.Context, clientID, secret, redirectURI string) KrogerAppTest {
	baseURL := kroger.BaseURL
	var transport http.RoundTripper
	if h.Kroger != nil {
		baseURL = h.Kroger.PublicBaseURL()
		transport = h.Kroger.PublicTransport()
	}
	ok, code, _ := kroger.TestAppKeys(ctx, baseURL, transport, clientID, secret, redirectURI)
	if !ok {
		if code == "" {
			code = "wrong_keys"
		}
		return KrogerAppTest{OK: false, Code: code}
	}
	if code != "" {
		return KrogerAppTest{OK: true, Code: code}
	}
	return KrogerAppTest{OK: true}
}

// Put handles PUT /api/households/{id}/kroger-app.
func (h *KrogerAppHandler) Put(req Request[krogerAppPutBody, krogerHouseholdParams]) (KrogerAppSaved, error) {
	scope, p, err := h.requireOwner(req.Context, req.PathParams.ID)
	if err != nil {
		return KrogerAppSaved{}, err
	}
	if h.Env.Disabled {
		return KrogerAppSaved{}, StoreDisabled()
	}
	existing, hadApp, err := h.Vault.Load(req.Context, scope, krogerProviderID)
	if err != nil {
		return KrogerAppSaved{}, InternalError(err)
	}
	clientID := strings.TrimSpace(req.Body.ClientID)
	secret := strings.TrimSpace(req.Body.ClientSecret)
	modality := strings.ToUpper(strings.TrimSpace(req.Body.Modality))
	if modality == "" && hadApp {
		modality = existing.Modality
	}
	if modality == "" {
		modality = "PICKUP"
	}
	if modality != "PICKUP" && modality != "DELIVERY" {
		return KrogerAppSaved{}, BadRequest("Modality must be PICKUP or DELIVERY.")
	}

	if clientID == "" && secret == "" {
		if !hadApp {
			return KrogerAppSaved{}, BadRequest("Client ID is required.")
		}
		if err := h.Vault.Save(req.Context, scope, appcred.Saved{
			ProviderID: krogerProviderID, ClientID: existing.ClientID, ClientSecret: existing.ClientSecret,
			RedirectURI: existing.RedirectURI, Modality: modality, ActorUserID: p.UserID,
			ProductAccess: existing.ProductAccess, ProductAccessAt: existing.ProductAccessAt,
		}); err != nil {
			return KrogerAppSaved{}, InternalError(err)
		}
		view, err := h.buildView(req.Context, scope, true)
		if err != nil {
			return KrogerAppSaved{}, err
		}
		return KrogerAppSaved{KrogerApp: view, Test: KrogerAppTest{OK: true}}, nil
	}
	if clientID == "" || secret == "" {
		return KrogerAppSaved{}, BadRequest("Client ID and client secret are required.")
	}
	if err := h.blockKrogerTest(scope.HouseholdID()); err != nil {
		return KrogerAppSaved{}, err
	}
	if err := h.noteKrogerTest(scope.HouseholdID()); err != nil {
		return KrogerAppSaved{}, err
	}
	redirectURI := h.serverRedirectURI()
	if redirectURI == "" {
		return KrogerAppSaved{}, BadRequest("This server has no public callback URL configured.")
	}

	test := h.runTest(req.Context, clientID, secret, redirectURI)
	if !test.OK {
		switch test.Code {
		case "kroger_unreachable":
			return KrogerAppSaved{}, StoreAppUnreachable()
		default:
			return KrogerAppSaved{}, StoreAppKeysRejected()
		}
	}

	var productAccess *int
	if test.Code == "no_product_access" {
		zero := 0
		productAccess = &zero
	} else {
		one := 1
		productAccess = &one
	}

	if err := h.saveApp(req.Context, scope, p.UserID, clientID, secret, redirectURI, modality, productAccess, hadApp, existing.ClientID); err != nil {
		return KrogerAppSaved{}, err
	}
	view, err := h.buildView(req.Context, scope, true)
	if err != nil {
		return KrogerAppSaved{}, err
	}
	return KrogerAppSaved{KrogerApp: view, Test: test}, nil
}

func (h *KrogerAppHandler) saveApp(ctx context.Context, scope tenant.Scope, actor, clientID, secret, redirectURI, modality string, productAccess *int, hadApp bool, oldClientID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return InternalError(err)
	}
	defer tx.Rollback()
	if err := h.Vault.SaveTx(ctx, tx, scope, appcred.Saved{
		ProviderID: krogerProviderID, ClientID: clientID, ClientSecret: secret,
		RedirectURI: redirectURI, Modality: modality, ActorUserID: actor,
		ProductAccess: productAccess, ProductAccessAt: now,
	}); err != nil {
		return InternalError(err)
	}
	if h.AfterCredentialSaveTestHook != nil {
		if err := h.AfterCredentialSaveTestHook(); err != nil {
			return InternalError(err)
		}
	}
	if !hadApp || oldClientID == clientID {
		if err := tx.Commit(); err != nil {
			return InternalError(err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE provider_connections SET state = 'reauth_required', access_token = NULL, refresh_token = NULL,
			access_token_expires_at = NULL, app_key = NULL, granted_scope = NULL
		WHERE household_id = ? AND provider_id = 'kroger'`, scope.HouseholdID()); err != nil {
		return InternalError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM store_availability WHERE household_id = ?`, scope.HouseholdID()); err != nil {
		return InternalError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM store_checks WHERE household_id = ?`, scope.HouseholdID()); err != nil {
		return InternalError(err)
	}
	if err := tx.Commit(); err != nil {
		return InternalError(err)
	}
	return nil
}

// Delete handles DELETE /api/households/{id}/kroger-app.
func (h *KrogerAppHandler) Delete(req Request[struct{}, krogerHouseholdParams]) (KrogerApp, error) {
	scope, p, err := h.requireOwner(req.Context, req.PathParams.ID)
	if err != nil {
		return KrogerApp{}, err
	}
	if h.Env.Disabled {
		return KrogerApp{}, StoreDisabled()
	}
	tx, err := h.DB.BeginTx(req.Context, nil)
	if err != nil {
		return KrogerApp{}, InternalError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(req.Context, `
		DELETE FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, scope.HouseholdID()); err != nil {
		return KrogerApp{}, InternalError(err)
	}
	if _, err := tx.ExecContext(req.Context, `
		DELETE FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, scope.HouseholdID()); err != nil {
		return KrogerApp{}, InternalError(err)
	}
	if _, err := tx.ExecContext(req.Context, `DELETE FROM store_availability WHERE household_id = ?`, scope.HouseholdID()); err != nil {
		return KrogerApp{}, InternalError(err)
	}
	if _, err := tx.ExecContext(req.Context, `DELETE FROM store_checks WHERE household_id = ?`, scope.HouseholdID()); err != nil {
		return KrogerApp{}, InternalError(err)
	}
	if err := tx.Commit(); err != nil {
		return KrogerApp{}, InternalError(err)
	}
	return h.buildView(req.Context, scope, p.Role == "owner")
}

// Test handles POST /api/households/{id}/kroger-app/test.
func (h *KrogerAppHandler) Test(req Request[krogerAppTestBody, krogerHouseholdParams]) (KrogerAppTest, error) {
	scope, _, err := h.requireOwner(req.Context, req.PathParams.ID)
	if err != nil {
		return KrogerAppTest{}, err
	}
	if h.Env.Disabled {
		return KrogerAppTest{}, StoreDisabled()
	}
	clientID := strings.TrimSpace(req.Body.ClientID)
	secret := strings.TrimSpace(req.Body.ClientSecret)
	if clientID == "" && secret == "" {
		saved, ok, err := h.Vault.Load(req.Context, scope, krogerProviderID)
		if err != nil {
			return KrogerAppTest{}, InternalError(err)
		}
		if !ok {
			return KrogerAppTest{}, KrogerNotConnected("not_configured")
		}
		if err := h.blockKrogerTest(scope.HouseholdID()); err != nil {
			return KrogerAppTest{}, err
		}
		if err := h.noteKrogerTest(scope.HouseholdID()); err != nil {
			return KrogerAppTest{}, err
		}
		clientID, secret = saved.ClientID, saved.ClientSecret
		redirectURI := saved.RedirectURI
		if redirectURI == "" {
			redirectURI = h.serverRedirectURI()
		}
		test := h.runTest(req.Context, clientID, secret, redirectURI)
		if test.OK {
			var pa *int
			if test.Code == "no_product_access" {
				z := 0
				pa = &z
			} else {
				o := 1
				pa = &o
			}
			now := time.Now().UTC().Format(time.RFC3339)
			if err := h.Vault.Save(req.Context, scope, appcred.Saved{
				ProviderID: krogerProviderID, ClientID: saved.ClientID, ClientSecret: saved.ClientSecret,
				RedirectURI: saved.RedirectURI, Modality: saved.Modality, ActorUserID: saved.ActorUserID,
				ProductAccess: pa, ProductAccessAt: now,
			}); err != nil {
				return KrogerAppTest{}, InternalError(err)
			}
		}
		return test, nil
	}
	if clientID == "" || secret == "" {
		return KrogerAppTest{}, BadRequest("Client ID and client secret are required.")
	}
	if err := h.blockKrogerTest(scope.HouseholdID()); err != nil {
		return KrogerAppTest{}, err
	}
	if err := h.noteKrogerTest(scope.HouseholdID()); err != nil {
		return KrogerAppTest{}, err
	}
	return h.runTest(req.Context, clientID, secret, h.serverRedirectURI()), nil
}
