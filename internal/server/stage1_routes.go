package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"database/sql"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/scan"
)

// WithStage1 turns on personal sessions, setup, and household membership.
// The listeners pass an entry so the chain can tell LAN from the public site.
func WithStage1(svc *auth.Service, site app.RuntimeConfig) Option {
	return func(c *config) {
		c.stage1svc = svc
		c.site = site
	}
}

func (s *stage1) db() *sql.DB {
	if s == nil || s.svc == nil {
		return nil
	}
	return s.svc.DB()
}

func registerStage1(apiMux *http.ServeMux, cfg *config) {
	var gate *stage1
	if cfg != nil {
		gate = cfg.gate
	}
	if gate == nil {
		gate = &stage1{}
	}
	apiMux.HandleFunc("GET /api/config", gate.serveConfig)
	apiMux.HandleFunc("GET /api/auth/session", gate.serveSession)
	apiMux.HandleFunc("POST /api/auth/login", gate.serveLogin)
	apiMux.HandleFunc("POST /api/auth/logout", gate.serveLogout)
	apiMux.HandleFunc("DELETE /api/sessions/{id}", gate.serveRevokeSession)
	apiMux.HandleFunc("POST /api/auth/reset", gate.serveReset)
	apiMux.HandleFunc("POST /api/auth/session/household", gate.serveSwitchHousehold)
	apiMux.HandleFunc("GET /api/auth/login", gate.serveStage3)
	apiMux.HandleFunc("GET /api/auth/callback", gate.serveStage3)
	apiMux.HandleFunc("GET /api/households", gate.serveListHouseholds)
	apiMux.HandleFunc("POST /api/households", gate.serveCreateHousehold)
	apiMux.HandleFunc("GET /api/households/{id}", gate.serveGetHousehold)
	apiMux.HandleFunc("PATCH /api/households/{id}", gate.serveRenameHousehold)
	apiMux.HandleFunc("DELETE /api/households/{id}", gate.serveDeleteHousehold)
	apiMux.HandleFunc("POST /api/households/{id}/restore", gate.serveRestoreHousehold)
	apiMux.HandleFunc("GET /api/households/{id}/members", gate.serveListMembers)
	apiMux.HandleFunc("PATCH /api/households/{id}/members/{userId}", gate.serveChangeRole)
	apiMux.HandleFunc("DELETE /api/households/{id}/members/{userId}", gate.serveRemoveMember)
	apiMux.HandleFunc("POST /api/households/{id}/members/{userId}/reset-link", gate.serveResetLink)
	apiMux.HandleFunc("GET /api/household/settings", gate.serveSettings)
	apiMux.HandleFunc("PATCH /api/household/settings", gate.servePatchSettings)
	apiMux.HandleFunc("POST /api/household/household-password/retire", gate.serveRetirePassword)
	apiMux.HandleFunc("POST /api/household/household-password/restore", gate.serveRestorePassword)
	apiMux.HandleFunc("POST /api/invites", gate.serveCreateInvite)
	apiMux.HandleFunc("GET /api/invites", gate.serveListInvites)
	apiMux.HandleFunc("POST /api/invites/{id}/revoke", gate.serveRevokeInvite)
	apiMux.HandleFunc("POST /api/invites/preview", gate.servePreviewInvite)
	apiMux.HandleFunc("POST /api/invites/accept", gate.serveAcceptInvite)
	apiMux.HandleFunc("POST /api/operator/household-invites", gate.serveOperatorInvite)
	apiMux.HandleFunc("GET /api/operator/alerts", gate.serveOperatorAlerts)
	apiMux.HandleFunc("POST /api/devices/pair", gate.serveStartPairing)
	apiMux.HandleFunc("GET /api/devices/pair/{id}", gate.servePairingStatus)
	apiMux.HandleFunc("POST /api/devices/pair/complete", gate.serveCompletePairing)
	apiMux.HandleFunc("GET /api/devices", gate.serveListDevices)
	apiMux.HandleFunc("PATCH /api/devices/{id}", gate.serveRenameDevice)
	apiMux.HandleFunc("DELETE /api/devices/{id}", gate.serveRevokeDevice)
	apiMux.HandleFunc("POST /api/device/scans", gate.serveDeviceScans)
	apiMux.HandleFunc("POST /api/device/heartbeat", gate.serveHeartbeat)
	apiMux.HandleFunc("POST /api/device/token/rotate", gate.serveRotateToken)
	apiMux.HandleFunc("DELETE /api/me", gate.serveDeleteAccount)
	apiMux.HandleFunc("POST /api/export", gate.serveCreateExport)
	apiMux.HandleFunc("GET /api/export/{id}", gate.serveExportStatus)
	apiMux.HandleFunc("GET /api/export/{id}/download", gate.serveExportZip)
	apiMux.HandleFunc("GET /api/export/{id}/stockbot-export.json", gate.serveExportJSON)
	apiMux.HandleFunc("GET /api/export/{id}/items.csv", gate.serveExportItems)
	apiMux.HandleFunc("GET /api/export/{id}/shopping-list.csv", gate.serveExportShopping)
	apiMux.HandleFunc("POST /api/household/exports", gate.serveCreateExport)
	apiMux.HandleFunc("GET /api/household/exports/{id}", gate.serveExportStatus)
	apiMux.HandleFunc("GET /api/household/exports/{id}/download", gate.serveExportZip)
	apiMux.HandleFunc("GET /api/households/{id}/export", gate.serveHouseholdExport)
	apiMux.HandleFunc("POST /api/household/import/uploads", gate.serveStage3)
	apiMux.HandleFunc("POST /api/import", gate.serveStage3)
	apiMux.HandleFunc("GET /api/import/{id}", gate.serveStage3)
}

func (s *stage1) unavailable(w http.ResponseWriter) bool {
	if s == nil || s.svc == nil {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return true
	}
	return false
}

func (s *stage1) account(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.unavailable(w) {
		return auth.Principal{}, false
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.Kind != "account" || p.UserID == "" {
		writeStage1(w, unauthorizedSentinel())
		return auth.Principal{}, false
	}
	return p, true
}

func (s *stage1) people(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.unavailable(w) {
		return auth.Principal{}, false
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || (p.Kind != "account" && p.Kind != "lan" && p.Kind != "household_password") {
		writeStage1(w, unauthorizedSentinel())
		return auth.Principal{}, false
	}
	if p.Kind == "account" && p.UserID == "" {
		writeStage1(w, unauthorizedSentinel())
		return auth.Principal{}, false
	}
	return p, true
}

func readStage1(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return false
	}
	return true
}

func countsAttempt(err error) bool {
	var ae *auth.Error
	if !errorsAs(err, &ae) || ae == nil {
		return false
	}
	switch ae.Code {
	case auth.CodeValidation, auth.CodeBusy, auth.CodeNoSpace, auth.CodeRateLimited:
		return false
	default:
		return true
	}
}

func (s *stage1) noteAttempt(err error, note func() error) {
	if err != nil && !countsAttempt(err) {
		return
	}
	if hit := note(); hit != nil && err == nil {
		return
	}
}

func sessionKindFor(r *http.Request) string {
	if expoClient(r) {
		return "bearer"
	}
	return "cookie"
}

func (s *stage1) publishSession(w http.ResponseWriter, r *http.Request, view auth.SessionView, status int) {
	token := view.Token
	if !expoClient(r) {
		view.Token = ""
	}
	s.setCookie(w, r, token)
	writeJSON(w, status, view)
}

func (s *stage1) serveStage3(w http.ResponseWriter, _ *http.Request) {
	writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
}

func (s *stage1) serveConfig(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	on, err := s.svc.HouseholdPasswordOn(r.Context())
	if err != nil {
		writeStage1(w, err)
		return
	}
	body := struct {
		Live             string `json:"live"`
		MinClientVersion struct {
			Expo string `json:"expo"`
		} `json:"minClientVersion"`
		HouseholdPasswordSignIn bool   `json:"householdPasswordSignIn"`
		AppBaseURL              string `json:"appBaseUrl,omitempty"`
		Entry                   string `json:"entry,omitempty"`
	}{
		Live:                    "sse",
		HouseholdPasswordSignIn: on,
		AppBaseURL:              s.site.AppBaseURL,
	}
	body.MinClientVersion.Expo = s.site.ExpoMinVersion
	if entry, ok := app.EntryFrom(r.Context()); ok {
		body.Entry = string(entry)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *stage1) serveSession(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.Kind == "" {
		writeJSON(w, http.StatusOK, auth.SessionView{Kind: "signed_out", Memberships: []auth.SessionHousehold{}, Households: []auth.HouseholdCard{}})
		return
	}
	view, err := s.svc.View(r.Context(), p)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type loginBody struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	HouseholdPassword string `json:"householdPassword"`
}

func (s *stage1) serveLogin(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	var body loginBody
	if !readStage1(w, r, &body) {
		return
	}
	account := body.Email != "" || body.Password != ""
	shared := body.HouseholdPassword != ""
	if account == shared {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	ip := stage1ClientIP(r)
	if account {
		if _, ok := normalizeLoginEmail(body.Email); !ok {
			writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: "email"})
			return
		}
		if len(body.Password) < 10 {
			writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentencePasswordShort, Field: "password"})
			return
		}
		if len(body.Password) > 256 {
			writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: "password"})
			return
		}
	}
	if err := s.svc.BlockLoginIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	if account {
		if err := s.svc.BlockLoginEmail(body.Email); err != nil {
			writeStage1(w, err)
			return
		}
	}
	var view auth.SessionView
	var err error
	if account {
		view, err = s.svc.LoginAccount(r.Context(), body.Email, body.Password, sessionKindFor(r))
	} else {
		view, err = s.svc.LoginHousehold(r.Context(), "", body.HouseholdPassword, sessionKindFor(r))
	}
	if err != nil {
		var ae *auth.Error
		if errorsAs(err, &ae) && ae.Count {
			_ = s.svc.FailLoginIP(ip)
			if account {
				_ = s.svc.FailLoginEmail(body.Email)
			}
		}
		writeStage1(w, err)
		return
	}
	s.svc.ClearLogin(ip, body.Email)
	s.publishSession(w, r, view, http.StatusOK)
}

func normalizeLoginEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || strings.ContainsAny(email, " \t\r\n") || !strings.Contains(email, "@") {
		return "", false
	}
	return email, true
}

func (s *stage1) serveLogout(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	if token := presentedToken(r, s.site); token != "" {
		_ = s.svc.RevokeSession(r.Context(), token)
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *stage1) serveRevokeSession(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	if err := s.svc.RevokeSessionByID(r.Context(), p.UserID, r.PathValue("id")); err != nil {
		writeStage1(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type resetBody struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

func (s *stage1) serveReset(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	var body resetBody
	if !readStage1(w, r, &body) {
		return
	}
	if body.Code == "" || len(body.Password) < 10 || len(body.Password) > 256 {
		field := ""
		if len(body.Password) < 10 {
			field = "password"
		}
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: field})
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockResetIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	view, err := s.svc.ResetPassword(r.Context(), body.Code, body.Password, sessionKindFor(r))
	if countsAttempt(err) || err == nil {
		_ = s.svc.NoteResetIP(ip)
	}
	if err != nil {
		writeStage1(w, err)
		return
	}
	s.publishSession(w, r, view, http.StatusOK)
}

type switchBody struct {
	HouseholdID string `json:"householdId"`
}

func (s *stage1) serveSwitchHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body switchBody
	if !readStage1(w, r, &body) {
		return
	}
	token := presentedToken(r, s.site)
	view, err := s.svc.SwitchHousehold(r.Context(), token, body.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	_ = p
	writeJSON(w, http.StatusOK, view)
}

func (s *stage1) serveListHouseholds(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	list, err := s.svc.ListHouseholds(r.Context(), p.UserID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	if list == nil {
		list = []auth.SessionHousehold{}
	}
	writeJSON(w, http.StatusOK, struct {
		Households []auth.SessionHousehold `json:"households"`
	}{Households: list})
}

type createHouseholdBody struct {
	Name        string `json:"name"`
	RestockMode string `json:"restockMode"`
	InviteCode  string `json:"inviteCode"`
}

func (s *stage1) serveCreateHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body createHouseholdBody
	if !readStage1(w, r, &body) {
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockInviteIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	token := presentedToken(r, s.site)
	detail, err := s.svc.CreateHousehold(r.Context(), p.UserID, token, body.Name, body.RestockMode, body.InviteCode)
	if err == nil || countsAttempt(err) {
		_ = s.svc.NoteInviteIP(ip)
	}
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}{ID: detail.ID, Name: detail.Name, Role: "owner"})
}

func (s *stage1) serveGetHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	detail, err := s.svc.GetHousehold(r.Context(), p.UserID, r.PathValue("id"))
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type nameBody struct {
	Name string `json:"name"`
}

func (s *stage1) serveRenameHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body nameBody
	if !readStage1(w, r, &body) {
		return
	}
	detail, err := s.svc.RenameHousehold(r.Context(), p.UserID, r.PathValue("id"), body.Name)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type confirmBody struct {
	Confirmation string `json:"confirmation"`
}

func (s *stage1) serveDeleteHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body confirmBody
	if !readStage1(w, r, &body) {
		return
	}
	detail, err := s.svc.DeleteHousehold(r.Context(), p.UserID, r.PathValue("id"), body.Confirmation)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *stage1) serveRestoreHousehold(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	detail, err := s.svc.RestoreHousehold(r.Context(), p.UserID, r.PathValue("id"))
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *stage1) serveListMembers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	members, err := s.svc.ListMembers(r.Context(), p.UserID, r.PathValue("id"))
	if err != nil {
		writeStage1(w, err)
		return
	}
	if members == nil {
		members = []auth.Member{}
	}
	writeJSON(w, http.StatusOK, struct {
		Members []auth.Member `json:"members"`
	}{Members: members})
}

type roleBody struct {
	Role string `json:"role"`
}

func (s *stage1) serveChangeRole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body roleBody
	if !readStage1(w, r, &body) {
		return
	}
	member, err := s.svc.ChangeRole(r.Context(), p.UserID, r.PathValue("id"), r.PathValue("userId"), body.Role)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (s *stage1) serveRemoveMember(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	if err := s.svc.RemoveMember(r.Context(), p.UserID, r.PathValue("id"), r.PathValue("userId")); err != nil {
		writeStage1(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *stage1) serveResetLink(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	link, err := s.svc.CreateResetLink(r.Context(), p.UserID, r.PathValue("id"), r.PathValue("userId"), p.Operator)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}{URL: s.site.AbsoluteURL("/reset/" + link.Code), ExpiresAt: link.ExpiresAt})
}

func (s *stage1) serveSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.people(w, r)
	if !ok {
		return
	}
	if p.HouseholdID == "" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: householdHeader})
		return
	}
	settings, err := s.svc.Settings(r.Context(), p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type settingsBody struct {
	RestockMode                   string  `json:"restockMode"`
	HouseholdPasswordEnabled      *bool   `json:"householdPasswordEnabled"`
	HouseholdPasswordRestoreUntil *string `json:"householdPasswordRestoreUntil"`
}

func (s *stage1) servePatchSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body settingsBody
	if !readStage1(w, r, &body) {
		return
	}
	if body.HouseholdPasswordEnabled != nil || body.HouseholdPasswordRestoreUntil != nil {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	settings, err := s.svc.UpdateRestock(r.Context(), p.UserID, p.HouseholdID, body.RestockMode)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *stage1) serveRetirePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	settings, err := s.svc.RetireHouseholdPassword(r.Context(), p.UserID, p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *stage1) serveRestorePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	settings, err := s.svc.RestoreHouseholdPassword(r.Context(), p.UserID, p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type inviteCreateBody struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *stage1) serveCreateInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body inviteCreateBody
	if r.ContentLength != 0 || r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
				return
			}
		}
	}
	created, err := s.svc.CreateJoinInvite(r.Context(), p.UserID, p.HouseholdID, body.Role)
	if err != nil {
		writeStage1(w, err)
		return
	}
	created.URL = s.site.AbsoluteURL("/invite/" + created.Code)
	writeJSON(w, http.StatusCreated, created)
}

func (s *stage1) serveListInvites(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	list, err := s.svc.ListInvites(r.Context(), p.UserID, p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	if list == nil {
		list = []auth.InviteSummary{}
	}
	writeJSON(w, http.StatusOK, struct {
		Invites []auth.InviteSummary `json:"invites"`
	}{Invites: list})
}

func (s *stage1) serveRevokeInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	if err := s.svc.RevokeInvite(r.Context(), p.UserID, revokeHouseholdID(r, p.HouseholdID), r.PathValue("id"), p.Operator); err != nil {
		writeStage1(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeHouseholdID is the household a join invite must belong to.
// A header wins over the session's active household. A start_household
// invite ignores this value.
func revokeHouseholdID(r *http.Request, active string) string {
	header := strings.TrimSpace(r.Header.Get(householdHeader))
	if header == "" {
		return active
	}
	if id := canonicalID(header); id != "" {
		return id
	}
	return header
}

type codeBody struct {
	Code string `json:"code"`
}

func (s *stage1) servePreviewInvite(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	var body codeBody
	if !readStage1(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockInviteIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	preview, err := s.svc.PreviewInvite(r.Context(), body.Code)
	if err == nil || countsAttempt(err) {
		_ = s.svc.NoteInviteIP(ip)
	}
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

type acceptBody struct {
	Code      string          `json:"code"`
	Account   *acceptAccount  `json:"account"`
	Household *acceptHouse    `json:"household"`
	Role      json.RawMessage `json:"role"`
}

type acceptAccount struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type acceptHouse struct {
	Name        string `json:"name"`
	RestockMode string `json:"restockMode"`
}

func (s *stage1) serveAcceptInvite(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	var body acceptBody
	if !readStage1(w, r, &body) {
		return
	}
	if len(body.Role) > 0 && string(body.Role) != "null" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: "role"})
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockInviteIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	var name, email, password, house, restock string
	if body.Account != nil {
		name, email, password = body.Account.Name, body.Account.Email, body.Account.Password
	}
	if body.Household != nil {
		house, restock = body.Household.Name, body.Household.RestockMode
	}
	signedIn := ""
	if p, ok := auth.PrincipalFrom(r.Context()); ok && p.Kind == "account" {
		signedIn = p.UserID
	}
	result, err := s.svc.AcceptInvite(r.Context(), signedIn, body.Code, name, email, password, house, restock, sessionKindFor(r))
	if err == nil || countsAttempt(err) {
		_ = s.svc.NoteInviteIP(ip)
	}
	if err != nil {
		writeStage1(w, err)
		return
	}
	token := result.Session.Token
	if !expoClient(r) {
		result.Session.Token = ""
	}
	s.setCookie(w, r, token)
	writeJSON(w, http.StatusOK, struct {
		auth.SessionView
		HouseholdID string `json:"householdId"`
		Name        string `json:"name"`
		Role        string `json:"role"`
	}{SessionView: result.Session, HouseholdID: result.HouseholdID, Name: result.Name, Role: result.Role})
}

// rejectOperatorInviteBody rejects a role, unknown fields, and invalid JSON.
// An empty body and {} are the stage-1 request. email, when sent, is ignored.
func rejectOperatorInviteBody(r *http.Request) error {
	if r == nil || r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var body struct {
		Email string          `json:"email"`
		Role  json.RawMessage `json:"role"`
	}
	if err := dec.Decode(&body); err != nil {
		return &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation}
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation}
	}
	if len(body.Role) > 0 {
		return &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: "role"}
	}
	return nil
}

func (s *stage1) serveOperatorInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	if err := rejectOperatorInviteBody(r); err != nil {
		writeStage1(w, err)
		return
	}
	created, err := s.svc.CreateStartInvite(r.Context(), p.UserID, p.Operator)
	if err != nil {
		writeStage1(w, err)
		return
	}
	created.URL = s.site.AbsoluteURL("/invite/" + created.Code)
	writeJSON(w, http.StatusCreated, created)
}

func (s *stage1) serveStartPairing(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	code, err := s.svc.StartPairing(r.Context(), p.UserID, p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, code)
}

func (s *stage1) servePairingStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	status, err := s.svc.PairingStatus(r.Context(), p.UserID, p.HouseholdID, r.PathValue("id"))
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type completePairBody struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Hub   *struct {
		Kind    string `json:"kind"`
		Version string `json:"version"`
		BootID  string `json:"bootId"`
	} `json:"hub"`
}

func (s *stage1) serveCompletePairing(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	var body completePairBody
	if !readStage1(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockPairIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	var hubKind, hubVersion, bootID string
	if body.Hub != nil {
		hubKind, hubVersion, bootID = body.Hub.Kind, body.Hub.Version, body.Hub.BootID
	}
	paired, err := s.svc.CompletePairing(r.Context(), body.Code, body.Label, hubKind, hubVersion, bootID)
	if err == nil || countsAttempt(err) {
		_ = s.svc.NotePairIP(ip)
	}
	if err != nil {
		writeStage1(w, err)
		return
	}
	resp := struct {
		DeviceID         string `json:"deviceId"`
		Label            string `json:"label,omitempty"`
		Token            string `json:"token"`
		Config           any    `json:"config,omitempty"`
		HeartbeatSeconds int    `json:"heartbeatSeconds,omitempty"`
	}{DeviceID: paired.DeviceID, Label: paired.Label, Token: paired.Token}
	if paired.Hub {
		resp.Config = s.hubConfig()
		resp.HeartbeatSeconds = 60
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *stage1) hubConfig() struct {
	ControlBarcodes struct {
		StockIn  string `json:"stockIn"`
		StockOut string `json:"stockOut"`
	} `json:"controlBarcodes"`
	IdleSeconds   int `json:"idleSeconds"`
	ConfigVersion int `json:"configVersion"`
} {
	var cfg struct {
		ControlBarcodes struct {
			StockIn  string `json:"stockIn"`
			StockOut string `json:"stockOut"`
		} `json:"controlBarcodes"`
		IdleSeconds   int `json:"idleSeconds"`
		ConfigVersion int `json:"configVersion"`
	}
	cfg.ControlBarcodes.StockIn = s.stockIn
	cfg.ControlBarcodes.StockOut = s.stockOut
	if cfg.ControlBarcodes.StockIn == "" {
		cfg.ControlBarcodes.StockIn = defaultStockInBarcode
	}
	if cfg.ControlBarcodes.StockOut == "" {
		cfg.ControlBarcodes.StockOut = defaultStockOutBarcode
	}
	cfg.IdleSeconds = int(scannerIdleTimeout.Seconds())
	cfg.ConfigVersion = 1
	return cfg
}

func (s *stage1) serveListDevices(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	list, err := s.svc.ListDevices(r.Context(), p.UserID, p.HouseholdID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	if list == nil {
		list = []auth.DeviceView{}
	}
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	if since == "" {
		writeJSON(w, http.StatusOK, struct {
			Devices []auth.DeviceView `json:"devices"`
		}{Devices: list})
		return
	}
	out, err := changes.Compare(r.Context(), s.db(), p.HouseholdID, changes.ScopeDevices, since)
	if err != nil {
		writeStage1(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !out.Changed {
		writeJSON(w, http.StatusOK, struct {
			Cursor  string `json:"cursor"`
			Changed bool   `json:"changed"`
		}{Cursor: out.Cursor, Changed: false})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Cursor  string            `json:"cursor"`
		Changed bool              `json:"changed"`
		Devices []auth.DeviceView `json:"devices"`
	}{Cursor: out.Cursor, Changed: true, Devices: list})
}

func (s *stage1) serveRenameDevice(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
		Name  string `json:"name"`
	}
	if !readStage1(w, r, &body) {
		return
	}
	label := body.Label
	if strings.TrimSpace(label) == "" {
		label = body.Name
	}
	device, err := s.svc.RenameDevice(r.Context(), p.UserID, p.HouseholdID, r.PathValue("id"), label)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (s *stage1) serveRevokeDevice(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	if err := s.svc.RevokeDevice(r.Context(), p.UserID, p.HouseholdID, r.PathValue("id")); err != nil {
		writeStage1(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *stage1) devicePrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.unavailable(w) {
		return auth.Principal{}, false
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.Kind != "device" {
		writeStage1(w, unauthorizedSentinel())
		return auth.Principal{}, false
	}
	return p, true
}

func (s *stage1) serveDeviceScans(w http.ResponseWriter, r *http.Request) {
	p, ok := s.devicePrincipal(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, deviceScanLimit+1))
	if err != nil {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	if len(body) > deviceScanLimit {
		writeStage1(w, &auth.Error{Code: auth.CodeTooLarge, Message: auth.SentenceTooLarge})
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	_, hasScans := raw["scans"]
	_, hasID := raw["id"]
	if hasScans && hasID {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	if hasScans {
		var batch struct {
			Scans []struct {
				ID        string          `json:"id"`
				Barcode   string          `json:"barcode"`
				ScannedAt json.RawMessage `json:"scannedAt"`
				Direction *string         `json:"direction"`
				UnitCount int             `json:"unitCount"`
				AgeMs     *int64          `json:"ageMs"`
				BootID    string          `json:"bootId"`
			} `json:"scans"`
			DroppedCount int `json:"droppedCount"`
		}
		if err := json.Unmarshal(body, &batch); err != nil || batch.DroppedCount < 0 {
			writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
			return
		}
		items := make([]auth.ScanInput, 0, len(batch.Scans))
		for _, item := range batch.Scans {
			items = append(items, scanInputFrom(item.ID, item.Barcode, item.ScannedAt, item.Direction, item.UnitCount, false, item.AgeMs, item.BootID))
		}
		results, err := s.svc.IngestScans(r.Context(), p, items, batch.DroppedCount)
		if err != nil {
			writeStage1(w, err)
			return
		}
		if results == nil {
			results = []auth.ScanResult{}
		}
		writeJSON(w, http.StatusOK, struct {
			Results []auth.ScanResult `json:"results"`
			Config  any               `json:"config"`
		}{Results: results, Config: s.hubConfig()})
		return
	}
	var one struct {
		ID        string          `json:"id"`
		Barcode   string          `json:"barcode"`
		ScannedAt json.RawMessage `json:"scannedAt"`
		Direction *string         `json:"direction"`
		UnitCount int             `json:"unitCount"`
		Heartbeat bool            `json:"heartbeat"`
		AgeMs     *int64          `json:"ageMs"`
		BootID    string          `json:"bootId"`
	}
	if err := json.Unmarshal(body, &one); err != nil {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	item := scanInputFrom(one.ID, one.Barcode, one.ScannedAt, one.Direction, one.UnitCount, one.Heartbeat, one.AgeMs, one.BootID)
	if rejectSingleScan(item) {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	results, err := s.svc.IngestScans(r.Context(), p, []auth.ScanInput{item}, 0)
	if err != nil {
		writeStage1(w, err)
		return
	}
	if len(results) == 0 {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	if results[0].Status == "rejected" {
		msg := auth.SentenceValidation
		if results[0].Message != "" {
			msg = results[0].Message
		}
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: msg})
		return
	}
	if results[0].Status == "conflict" {
		writeStage1(w, &auth.Error{Code: auth.CodeConflict, Message: "scan id already used with different payload"})
		return
	}
	status := results[0].Status
	if status == "created" {
		status = "accepted"
	}
	writeJSON(w, http.StatusOK, struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Cursor string `json:"cursor"`
	}{ID: results[0].ID, Status: status, Cursor: results[0].Cursor})
}

func scanInputFrom(id, barcode string, scannedAt json.RawMessage, direction *string, unit int, heartbeat bool, age *int64, bootID string) auth.ScanInput {
	item := auth.ScanInput{
		ID: id, Barcode: barcode, UnitCount: unit, Heartbeat: heartbeat,
		HasBarcode: scan.TrimASCII(barcode) != "", AgeMs: age, BootID: bootID,
	}
	if direction != nil {
		item.Direction = *direction
	}
	if len(scannedAt) == 0 || string(scannedAt) == "null" {
		return item
	}
	var text string
	if err := json.Unmarshal(scannedAt, &text); err != nil {
		item.ScannedAtInvalid = true
		return item
	}
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		item.ScannedAtInvalid = true
		return item
	}
	item.HasScannedAt = true
	item.ScannedAt = at
	return item
}

func rejectSingleScan(item auth.ScanInput) bool {
	if item.ID == "" || item.ScannedAtInvalid {
		return true
	}
	if item.Heartbeat && !item.HasBarcode {
		return false
	}
	if !item.HasBarcode {
		return true
	}
	barcode := scan.TrimASCII(item.Barcode)
	if barcode == "" || len(barcode) > 64 || !printableScan(barcode) || scan.LooksLikePairingCode(item.Barcode) {
		return true
	}
	if item.Direction != "" && item.Direction != "stock_in" && item.Direction != "stock_out" {
		return true
	}
	if item.UnitCount < 0 || item.UnitCount > 999 {
		return true
	}
	return false
}

func printableScan(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7E {
			return false
		}
	}
	return true
}

type heartbeatBody struct {
	Version      string `json:"version"`
	BootID       string `json:"bootId"`
	UptimeS      int    `json:"uptimeS"`
	Queued       int    `json:"queued"`
	DroppedCount int    `json:"droppedCount"`
	ClockSynced  bool   `json:"clockSynced"`
	Rollback     bool   `json:"rollback"`
}

func (s *stage1) serveHeartbeat(w http.ResponseWriter, r *http.Request) {
	p, ok := s.devicePrincipal(w, r)
	if !ok {
		return
	}
	var body heartbeatBody
	if !readStage1(w, r, &body) {
		return
	}
	rotate, err := s.svc.Heartbeat(r.Context(), p, auth.Heartbeat{
		Version: body.Version, BootID: body.BootID, UptimeS: body.UptimeS,
		Queued: body.Queued, Dropped: body.DroppedCount, ClockSynced: body.ClockSynced,
		Rollback: body.Rollback,
	})
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ServerTime       string `json:"serverTime"`
		Config           any    `json:"config"`
		HeartbeatSeconds int    `json:"heartbeatSeconds"`
		RotateToken      bool   `json:"rotateToken"`
	}{
		ServerTime:       s.svc.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Config:           s.hubConfig(),
		HeartbeatSeconds: 60,
		RotateToken:      rotate,
	})
}

func (s *stage1) serveRotateToken(w http.ResponseWriter, r *http.Request) {
	if s.unavailable(w) {
		return
	}
	token := bearerToken(r)
	next, err := s.svc.RotateDeviceToken(r.Context(), token)
	if err != nil {
		writeStage1(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token      string `json:"token"`
		GraceUntil string `json:"graceUntil,omitempty"`
	}{Token: next.Token, GraceUntil: next.GraceUntil})
}

func (s *stage1) serveOperatorAlerts(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	list, err := s.svc.ListOperatorAlerts(r.Context(), p.UserID)
	if err != nil {
		writeStage1(w, err)
		return
	}
	if list == nil {
		list = []auth.OperatorAlert{}
	}
	writeJSON(w, http.StatusOK, struct {
		Alerts []auth.OperatorAlert `json:"alerts"`
	}{Alerts: list})
}

func (s *stage1) serveDeleteAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := s.account(w, r)
	if !ok {
		return
	}
	var body confirmBody
	if !readStage1(w, r, &body) {
		return
	}
	if err := s.svc.DeleteAccount(r.Context(), p.UserID, body.Confirmation); err != nil {
		writeStage1(w, err)
		return
	}
	if token := presentedToken(r, s.site); token != "" {
		_ = s.svc.RevokeSession(r.Context(), token)
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *stage1) serveSetupPage(w http.ResponseWriter, r *http.Request, next http.Handler) {
	entry, _ := app.EntryFrom(r.Context())
	if entry != app.EntryLAN {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return
	}
	if err := s.svc.Ensure(r.Context()); err != nil {
		writeStage1(w, err)
		return
	}
	has, err := s.svc.HasAccount(r.Context())
	if err != nil {
		writeStage1(w, err)
		return
	}
	mode, valid, err := s.svc.ActiveSetupMode(r.Context())
	if err != nil {
		writeStage1(w, err)
		return
	}
	if has && !(valid && mode == "recover") {
		if s.site.AppBaseURL != "" {
			http.Redirect(w, r, s.site.AppBaseURL, http.StatusFound)
			return
		}
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return
	}
	next.ServeHTTP(w, r)
}

type setupBody struct {
	SetupCode   string `json:"setupCode"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	NewPassword string `json:"newPassword"`
}

func (s *stage1) serveSetupPost(w http.ResponseWriter, r *http.Request) {
	entry, _ := app.EntryFrom(r.Context())
	if entry != app.EntryLAN {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return
	}
	var body setupBody
	if !readStage1(w, r, &body) {
		return
	}
	recover := body.NewPassword != ""
	if body.SetupCode == "" || body.Email == "" || (recover && (body.Password != "" || strings.TrimSpace(body.Name) != "")) || (!recover && (body.Password == "" || strings.TrimSpace(body.Name) == "")) {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation})
		return
	}
	ip := stage1ClientIP(r)
	if err := s.svc.BlockLoginIP(ip); err != nil {
		writeStage1(w, err)
		return
	}
	err := s.svc.RedeemSetup(r.Context(), body.SetupCode, body.Name, body.Email, body.Password, body.NewPassword)
	if err != nil {
		var ae *auth.Error
		if errorsAs(err, &ae) && ae.Code == auth.CodeNotFound {
			_ = s.svc.FailLoginIP(ip)
		}
		writeStage1(w, err)
		return
	}
	s.svc.ClearLogin(ip, "")
	writeJSON(w, http.StatusOK, struct{}{})
}
