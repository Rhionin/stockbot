package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

// ProvidersHandler handles provider-related operations.
type ProvidersHandler struct {
	Registry      *cart.Registry
	ConnectionDir *connection.Directory
	Credentials   *appcred.Vault
	EnvFallback   ProviderEnv
}

// ProviderEnv is the deploy-time credential fallback. It is never written to a
// response. A saved row overrides it until that row is cleared.
type ProviderEnv struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Modality     string
	Disabled     bool
}

// Complete reports whether the environment can configure a provider by itself.
func (e ProviderEnv) Complete() bool {
	return !e.Disabled && e.ClientID != "" && e.ClientSecret != "" && e.RedirectURI != ""
}

// ProviderInfo represents information about a registered provider.
type ProviderInfo struct {
	ID                    string               `json:"id"`
	DisplayName           string               `json:"displayName"`
	Capabilities          ProviderCapabilities `json:"capabilities"`
	ConnectionState       string               `json:"connectionState"`
	CredentialsConfigured bool                 `json:"credentialsConfigured"`
	Credentials           *CredentialView      `json:"credentials,omitempty"`
}

// CredentialView is safe to return from GET. It never includes a client secret.
type CredentialView struct {
	ClientID    string `json:"clientId"`
	RedirectURI string `json:"redirectUri"`
	Modality    string `json:"modality"`
	SecretSet   bool   `json:"secretSet"`
	Source      string `json:"source"`
}

// ProviderCapabilities represents the five capability dimensions of a provider.
type ProviderCapabilities struct {
	Auth         string `json:"auth"`
	Delivery     string `json:"delivery"`
	Confirmation string `json:"confirmation"`
	Mutation     string `json:"mutation"`
	Identity     string `json:"identity"`
}

// ListProvidersHandler handles GET /api/providers.
// Returns information about all registered providers.
type ListProvidersHandler struct {
	ProvidersHandler
}

// Handle returns a list of all registered providers.
func (h *ListProvidersHandler) Handle(req Request[struct{}, struct{}]) ([]ProviderInfo, error) {
	if h.Registry == nil {
		return []ProviderInfo{}, nil
	}
	ids := h.Registry.List()
	infos := make([]ProviderInfo, 0, len(ids))
	for _, id := range ids {
		provider, ok := h.Registry.Get(id)
		if !ok {
			continue
		}
		caps := provider.Capabilities()
		state, err := providerConnectionState(req.Context, h.ConnectionDir, string(id), caps.Auth)
		if err != nil {
			return nil, InternalError(err)
		}
		info := ProviderInfo{
			ID:          string(id),
			DisplayName: provider.DisplayName(),
			Capabilities: ProviderCapabilities{
				Auth:         string(caps.Auth),
				Delivery:     string(caps.Delivery),
				Confirmation: string(caps.Confirmation),
				Mutation:     string(caps.Mutation),
				Identity:     string(caps.Identity),
			},
			ConnectionState:       state,
			CredentialsConfigured: h.Registry.CredentialsConfigured(id),
		}
		if _, ok := provider.(cart.AppCredentialSink); ok {
			view, viewErr := h.credentialView(req.Context, provider)
			if viewErr != nil {
				return nil, InternalError(viewErr)
			}
			info.Credentials = &view
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func providerConnectionState(ctx context.Context, dir *connection.Directory, providerID string, auth cart.AuthCapability) (string, error) {
	if auth == cart.AuthNone {
		return string(connection.StateNotRequired), nil
	}
	if dir == nil {
		return string(connection.StateDisconnected), nil
	}
	conn, err := dir.Read(ctx, providerID)
	if err != nil {
		return "", err
	}
	if conn == nil || conn.State == "" {
		return string(connection.StateDisconnected), nil
	}
	return string(conn.State), nil
}

// ProviderAuthorizeHandler handles GET /api/providers/{providerId}/authorize.
// Initiates the OAuth2 authorization flow.
type ProviderAuthorizeHandler struct {
	ProvidersHandler
}

type ProviderAuthorizeRequest struct {
	ProviderID string `json:"providerId"`
}

type ProviderAuthorizeResponse struct {
	AuthorizationURL string `json:"authorizationUrl"`
}

// Handle initiates the OAuth2 authorization flow.
func (h *ProviderAuthorizeHandler) Handle(req Request[struct{}, ProviderAuthorizeRequest]) (*ProviderAuthorizeResponse, error) {
	providerID := cart.ProviderID(req.PathParams.ProviderID)

	// Get the provider from registry
	provider, ok := h.Registry.Get(providerID)
	if !ok {
		return nil, NotFound(fmt.Sprintf("provider %q not registered", providerID))
	}

	caps := provider.Capabilities()
	if caps.Auth != cart.AuthOAuth2 {
		return nil, BadRequest(fmt.Sprintf("%s does not use an authorization-code connection", provider.DisplayName()))
	}
	if !h.Registry.CredentialsConfigured(providerID) {
		return nil, Conflict(fmt.Sprintf("%s is not configured", provider.DisplayName()))
	}
	flow, ok := provider.(cart.OAuthFlow)
	if !ok {
		return nil, InternalError(fmt.Errorf("%s cannot start a connection", provider.DisplayName()))
	}

	state, err := newAuthState()
	if err != nil {
		return nil, InternalError(err)
	}
	if err := h.ConnectionDir.GenerateAuthState(req.Context, string(providerID), state); err != nil {
		return nil, InternalError(err)
	}
	authorizationURL, err := flow.AuthorizationURL(state)
	if err != nil {
		return nil, InternalError(err)
	}
	return &ProviderAuthorizeResponse{AuthorizationURL: authorizationURL}, nil
}

func newAuthState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ProviderCallbackHandler handles GET /api/providers/{providerId}/callback.
// Processes the OAuth2 callback and exchanges the code for tokens.
type ProviderCallbackHandler struct {
	ProvidersHandler
}

type ProviderCallbackRequest struct {
	ProviderID string `json:"providerId"`
	Code       string `query:"code"`
	State      string `query:"state"`
}

// Handle processes the OAuth2 callback and sends the browser back to the
// shopping list. The redirect carries no token.
func (h *ProviderCallbackHandler) Handle(req Request[struct{}, ProviderCallbackRequest]) (SeeOther, error) {
	providerID := cart.ProviderID(req.PathParams.ProviderID)

	provider, ok := h.Registry.Get(providerID)
	if !ok {
		return SeeOther{}, NotFound(fmt.Sprintf("provider %q is not registered", providerID))
	}
	if provider.Capabilities().Auth != cart.AuthOAuth2 {
		return SeeOther{}, BadRequest(fmt.Sprintf("%s does not use an authorization-code connection", provider.DisplayName()))
	}
	flow, ok := provider.(cart.OAuthFlow)
	if !ok {
		return SeeOther{}, InternalError(fmt.Errorf("%s cannot finish a connection", provider.DisplayName()))
	}

	query := req.RawRequest.URL.Query()
	if query.Get("error") != "" {
		_, _ = h.ConnectionDir.ConsumeAuthState(req.Context, string(providerID), query.Get("state"))
		return SeeOther{}, BadRequest("the provider declined the connection")
	}

	valid, err := h.ConnectionDir.ConsumeAuthState(req.Context, string(providerID), query.Get("state"))
	if err != nil {
		return SeeOther{}, InternalError(err)
	}
	if !valid {
		return SeeOther{}, BadRequest("invalid or expired authorization state")
	}

	tokenSet, err := flow.ExchangeCode(req.Context, query.Get("code"))
	if err != nil {
		// Do not include err: a provider body is where tokens live.
		log.Printf("provider %s authorization code exchange failed", providerID)
		return SeeOther{}, BadGateway(fmt.Sprintf("%s rejected the authorization code", provider.DisplayName()))
	}

	expiresAt := tokenSet.ReceiptAt.Add(tokenSet.ExpiresIn)
	conn := &connection.Connection{
		Provider:     string(providerID),
		State:        connection.StateConnected,
		AccessToken:  tokenSet.AccessToken,
		RefreshToken: tokenSet.RefreshToken,
		ExpiresAt:    &expiresAt,
	}
	if err := h.ConnectionDir.Write(req.Context, conn); err != nil {
		return SeeOther{}, InternalError(err)
	}

	return SeeOther{Location: "/shopping?connected=" + url.QueryEscape(string(providerID))}, nil
}

// ProviderDisconnectHandler handles DELETE /api/providers/{providerId}/connection.
// Disconnects the provider and clears credentials.
type ProviderDisconnectHandler struct {
	ProvidersHandler
}

type ProviderDisconnectRequest struct {
	ProviderID string `json:"providerId"`
}

// Handle disconnects the provider.
func (h *ProviderDisconnectHandler) Handle(req Request[struct{}, ProviderDisconnectRequest]) (Created, error) {
	providerID := cart.ProviderID(req.PathParams.ProviderID)

	// Get the provider from registry
	provider, ok := h.Registry.Get(providerID)
	if !ok {
		return Created{}, NotFound(fmt.Sprintf("provider %q not registered", providerID))
	}

	caps := provider.Capabilities()

	// Check if provider requires authentication
	if caps.Auth == cart.AuthNone {
		return Created{}, BadRequest(fmt.Sprintf("provider %q does not require authentication", providerID))
	}

	// Disconnect the provider
	if err := h.ConnectionDir.Disconnect(req.Context, string(providerID)); err != nil {
		return Created{}, InternalError(fmt.Errorf("failed to disconnect: %w", err))
	}

	return Created{Value: struct{}{}}, nil
}

// ProviderLedgerHandler handles ledger operations for providers.
type ProviderLedgerHandler struct {
	ProvidersHandler
	Ledger *cart.Ledger
}

// ProviderLedgerGetHandler handles GET /api/providers/{providerId}/ledger.
type ProviderLedgerGetHandler struct {
	ProviderLedgerHandler
}

type ProviderLedgerGetRequest struct {
	ProviderID string `json:"providerId"`
}

type LedgerEntry struct {
	ProviderID     string `json:"providerId"`
	ItemID         string `json:"itemId"`
	Requested      int    `json:"requested"`
	LedgerBoundary string `json:"ledgerBoundary"`
}

type ProviderLedgerResponse struct {
	ProviderID string        `json:"providerId"`
	Entries    []LedgerEntry `json:"entries"`
}

// Handle returns the ledger entries for a provider.
func (h *ProviderLedgerGetHandler) Handle(req Request[struct{}, ProviderLedgerGetRequest]) (*ProviderLedgerResponse, error) {
	providerID := cart.ProviderID(req.PathParams.ProviderID)

	// Get the provider from registry
	provider, ok := h.Registry.Get(providerID)
	if !ok {
		return nil, NotFound(fmt.Sprintf("provider %q not registered", providerID))
	}

	caps := provider.Capabilities()

	// Check if provider requires authentication
	if caps.Auth != cart.AuthNone {
		conn, err := h.ConnectionDir.Read(req.Context, string(providerID))
		if err != nil {
			return nil, InternalError(fmt.Errorf("failed to read connection: %w", err))
		}
		if conn == nil {
			return nil, Conflict(fmt.Sprintf("provider %q is not connected", providerID))
		}
		if conn.State != connection.StateConnected {
			return nil, Conflict(fmt.Sprintf("provider %q is not connected", providerID))
		}
	}

	// Get ledger entries
	ledgerMap, err := h.Ledger.ListForProvider(req.Context, providerID)
	if err != nil {
		return nil, InternalError(fmt.Errorf("failed to read ledger: %w", err))
	}

	// Convert to response format
	entries := make([]LedgerEntry, 0, len(ledgerMap))
	for itemID, entry := range ledgerMap {
		entries = append(entries, LedgerEntry{
			ProviderID:     string(providerID),
			ItemID:         itemID,
			Requested:      entry.Requested,
			LedgerBoundary: entry.Boundary.UTC().Format("2006-01-02T15:04:05.000000000Z"),
		})
	}

	return &ProviderLedgerResponse{
		ProviderID: string(providerID),
		Entries:    entries,
	}, nil
}

// ProviderLedgerResetHandler handles POST /api/providers/{providerId}/ledger/reset.
type ProviderLedgerResetHandler struct {
	ProviderLedgerHandler
	ShoppingList interface {
		EndTrip(ctx context.Context, userID string) error
	}
}

type ProviderLedgerResetRequest struct {
	ProviderID string `json:"providerId"`
}

// Handle resets the ledger for a provider.
func (h *ProviderLedgerResetHandler) Handle(req Request[struct{}, ProviderLedgerResetRequest]) (Created, error) {
	providerID := cart.ProviderID(req.PathParams.ProviderID)

	// Get the provider from registry
	provider, ok := h.Registry.Get(providerID)
	if !ok {
		return Created{}, NotFound(fmt.Sprintf("provider %q not registered", providerID))
	}

	caps := provider.Capabilities()

	// Check if provider requires authentication
	if caps.Auth != cart.AuthNone {
		conn, err := h.ConnectionDir.Read(req.Context, string(providerID))
		if err != nil {
			return Created{}, InternalError(fmt.Errorf("failed to read connection: %w", err))
		}
		if conn == nil {
			return Created{}, Conflict(fmt.Sprintf("provider %q is not connected", providerID))
		}
		if conn.State != connection.StateConnected {
			return Created{}, Conflict(fmt.Sprintf("provider %q is not connected", providerID))
		}
	}

	// Reset ledger
	if err := h.Ledger.ResetForProvider(req.Context, providerID); err != nil {
		return Created{}, InternalError(fmt.Errorf("failed to reset ledger: %w", err))
	}
	// A new cart is a new trip. Checked-off lines from the previous trip
	// no longer block the next list.
	if h.ShoppingList != nil {
		if err := h.ShoppingList.EndTrip(req.Context, "user-1"); err != nil {
			return Created{}, InternalError(err)
		}
	}

	return Created{Value: struct{}{}}, nil
}
