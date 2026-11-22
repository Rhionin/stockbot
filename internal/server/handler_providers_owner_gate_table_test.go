package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

type ownerGateRouteKind int

const (
	ownerGateRouteOAuthAuthorize ownerGateRouteKind = iota
	ownerGateRouteOAuthCallback
	ownerGateRouteOAuthDisconnect
	ownerGateRouteKrogerAuthorize
	ownerGateRouteKrogerCallbackShopping
	ownerGateRouteKrogerCallbackHousehold
	ownerGateRouteStoresCanConnect
)

type ownerGateTableRoute struct {
	name string
	kind ownerGateRouteKind
}

type ownerGateCaseKind int

const (
	ownerGateCaseMemberDenied ownerGateCaseKind = iota
	ownerGateCaseLookupError
	ownerGateCaseOwnerAllowed
)

type ownerGateTableCase struct {
	name string
	kind ownerGateCaseKind
}

type callbackConnectionSeed int

const (
	callbackSeedNone callbackConnectionSeed = iota
	callbackSeedConnected
	callbackSeedReauthRequired
)

func TestProviderConnectRequiresOwner(t *testing.T) {
	TestProviderConnectOwnerGateTable(t)
}

func TestProviderConnectOwnerGateTable(t *testing.T) {
	routes := []ownerGateTableRoute{
		{name: "oauth_authorize", kind: ownerGateRouteOAuthAuthorize},
		{name: "oauth_callback", kind: ownerGateRouteOAuthCallback},
		{name: "oauth_disconnect", kind: ownerGateRouteOAuthDisconnect},
		{name: "kroger_authorize", kind: ownerGateRouteKrogerAuthorize},
		{name: "kroger_callback_shopping", kind: ownerGateRouteKrogerCallbackShopping},
		{name: "kroger_callback_household", kind: ownerGateRouteKrogerCallbackHousehold},
		{name: "stores_can_connect", kind: ownerGateRouteStoresCanConnect},
	}
	cases := []ownerGateTableCase{
		{name: "member_denied", kind: ownerGateCaseMemberDenied},
		{name: "lookup_error_fails_closed", kind: ownerGateCaseLookupError},
		{name: "owner_allowed", kind: ownerGateCaseOwnerAllowed},
	}
	for _, route := range routes {
		for _, tc := range cases {
			seeds := []callbackConnectionSeed{callbackSeedNone}
			if routeUsesCallbackCredentialSeed(route, tc) {
				seeds = []callbackConnectionSeed{callbackSeedConnected, callbackSeedReauthRequired}
			}
			for _, seed := range seeds {
				name := route.name + "/" + tc.name
				if seed != callbackSeedNone {
					name += "/" + callbackSeedName(seed)
				}
				seed := seed
				t.Run(name, func(t *testing.T) {
					runOwnerGateTable(t, route, tc, seed)
				})
			}
		}
	}
}

func routeUsesCallbackCredentialSeed(route ownerGateTableRoute, tc ownerGateTableCase) bool {
	if tc.kind != ownerGateCaseMemberDenied && tc.kind != ownerGateCaseLookupError {
		return false
	}
	switch route.kind {
	case ownerGateRouteOAuthCallback, ownerGateRouteKrogerCallbackShopping, ownerGateRouteKrogerCallbackHousehold:
		return true
	default:
		return false
	}
}

func callbackSeedName(seed callbackConnectionSeed) string {
	switch seed {
	case callbackSeedConnected:
		return "seed_connected"
	case callbackSeedReauthRequired:
		return "seed_reauth_required"
	default:
		return "seed_none"
	}
}

func runOwnerGateTable(t *testing.T, route ownerGateTableRoute, tc ownerGateTableCase, seed callbackConnectionSeed) {
	t.Helper()
	switch route.kind {
	case ownerGateRouteOAuthAuthorize, ownerGateRouteOAuthCallback, ownerGateRouteOAuthDisconnect:
		e := setupProviderConnectGateEnv(t)
		runOwnerGateTableOAuth(t, e, route, tc, seed)
	case ownerGateRouteKrogerAuthorize, ownerGateRouteKrogerCallbackShopping, ownerGateRouteKrogerCallbackHousehold:
		e := setupKrogerConnectGateEnv(t)
		runOwnerGateTableKroger(t, e, route, tc, seed)
	case ownerGateRouteStoresCanConnect:
		e := setupKrogerConnectGateEnv(t)
		runOwnerGateTableStores(t, e, tc)
	default:
		t.Fatalf("unknown route %d", route.kind)
	}
}

func membershipErrHook() func(context.Context, string, string) (string, string, bool, error) {
	return func(context.Context, string, string) (string, string, bool, error) {
		return "", "", false, errors.New("membership lookup failed")
	}
}

func assertExactLocation(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	got := rec.Header().Get("Location")
	if got != want {
		t.Fatalf("Location got %q want %q", got, want)
	}
}

func snapshotConnection(t *testing.T, db *sql.DB, house, provider string) *connection.Connection {
	t.Helper()
	conn, err := connection.NewDirectory(db).Read(context.Background(), mustHouseholdScope(t, house), provider)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func connectionCredentialsUnchanged(before, after *connection.Connection) bool {
	if before == nil && after == nil {
		return true
	}
	if before == nil || after == nil {
		return false
	}
	return before.State == after.State &&
		before.AccessToken == after.AccessToken &&
		before.RefreshToken == after.RefreshToken &&
		before.AppKey == after.AppKey
}

func seedProviderLinkForGate(t *testing.T, db *sql.DB, house, provider string, st connection.ConnectionState, appKey string) {
	t.Helper()
	scope := mustHouseholdScope(t, house)
	if err := connection.NewDirectory(db).Write(context.Background(), scope, &connection.Connection{
		Provider:     provider,
		State:        st,
		AccessToken:  "gate-seed-access",
		RefreshToken: "gate-seed-refresh",
		AppKey:       appKey,
	}); err != nil {
		t.Fatal(err)
	}
}

func krogerHouseholdAppKey(t *testing.T, db *sql.DB, house string) string {
	t.Helper()
	vault := appcred.NewVault(db)
	scope := mustHouseholdScope(t, house)
	_, appKey, err := connection.AppFor(context.Background(), vault, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return appKey
}

func runOwnerGateTableOAuth(t *testing.T, e *providerConnectGateEnv, route ownerGateTableRoute, tc ownerGateTableCase, seed callbackConnectionSeed) {
	t.Helper()
	provider := providerConnectGateID
	switch route.kind {
	case ownerGateRouteOAuthAuthorize:
		switch tc.kind {
		case ownerGateCaseMemberDenied:
			rec := e.member.authed(http.MethodGet, e.authorizePath(), nil)
			assertForbiddenRole(t, rec)
			if got := providerConnectAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("member authorize minted auth state %q", got)
			}
		case ownerGateCaseLookupError:
			rec := ownerGateRequest(t, e, http.MethodGet, e.authorizePath(), membershipErrHook())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("authorize lookup error %d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "authorizationUrl") {
				t.Fatalf("lookup error succeeded: %s", rec.Body.String())
			}
			if got := providerConnectAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("lookup error minted auth state %q", got)
			}
		case ownerGateCaseOwnerAllowed:
			rec := e.owner.authed(http.MethodGet, e.authorizePath(), nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "authorizationUrl") {
				t.Fatalf("owner authorize %d %s", rec.Code, rec.Body.String())
			}
		}
	case ownerGateRouteOAuthCallback:
		state, callbackURL, connBefore := oauthCallbackFixture(t, e, seed)
		switch tc.kind {
		case ownerGateCaseMemberDenied:
			rec := e.member.authed(http.MethodGet, callbackURL, nil)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("member callback %d", rec.Code)
			}
			assertExactLocation(t, rec, "/shopping?"+provider+"=not_allowed")
			if got := providerConnectAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("member callback left auth state %q", got)
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if !connectionCredentialsUnchanged(connBefore, connAfter) {
				t.Fatalf("connection changed before=%+v after=%+v", connBefore, connAfter)
			}
			replay := e.owner.authed(http.MethodGet, callbackURL, nil)
			if replay.Code != http.StatusBadRequest ||
				!strings.Contains(replay.Body.String(), "invalid or expired authorization state") {
				t.Fatalf("owner replay %d %s", replay.Code, replay.Body.String())
			}
		case ownerGateCaseLookupError:
			_ = state
			rec := ownerGateRequest(t, e, http.MethodGet, callbackURL, membershipErrHook())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("callback lookup error %d %s", rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); strings.Contains(loc, "not_allowed") || strings.Contains(loc, "connected") {
				t.Fatalf("callback lookup error redirected %q", loc)
			}
			if got := providerConnectAuthState(t, e.db, e.house); got != state {
				t.Fatalf("callback lookup error changed auth state before=%q after=%q", state, got)
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if !connectionCredentialsUnchanged(connBefore, connAfter) {
				t.Fatalf("connection changed before=%+v after=%+v", connBefore, connAfter)
			}
		case ownerGateCaseOwnerAllowed:
			_ = state
			rec := e.owner.authed(http.MethodGet, callbackURL, nil)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("owner callback %d", rec.Code)
			}
			assertExactLocation(t, rec, "/shopping?connected="+provider)
		}
	case ownerGateRouteOAuthDisconnect:
		seedProviderConnectConnection(t, e.db, e.house)
		connBefore := snapshotConnection(t, e.db, e.house, provider)
		switch tc.kind {
		case ownerGateCaseMemberDenied:
			rec := e.member.authed(http.MethodDelete, e.disconnectPath(), nil)
			assertForbiddenRole(t, rec)
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if connAfter == nil || connAfter.State != connection.StateConnected {
				t.Fatalf("member disconnect cleared connection before=%+v after=%+v", connBefore, connAfter)
			}
		case ownerGateCaseLookupError:
			rec := ownerGateRequest(t, e, http.MethodDelete, e.disconnectPath(), membershipErrHook())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("disconnect lookup error %d %s", rec.Code, rec.Body.String())
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if connAfter == nil || connAfter.State != connection.StateConnected {
				t.Fatalf("disconnect lookup error cleared connection before=%+v after=%+v", connBefore, connAfter)
			}
		case ownerGateCaseOwnerAllowed:
			rec := e.owner.authed(http.MethodDelete, e.disconnectPath(), nil)
			if rec.Code != http.StatusCreated {
				t.Fatalf("owner disconnect %d %s", rec.Code, rec.Body.String())
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if connAfter == nil || connAfter.State != connection.StateDisconnected {
				t.Fatalf("owner disconnect did not disconnect conn=%+v", connAfter)
			}
		}
	}
}

func oauthCallbackFixture(t *testing.T, e *providerConnectGateEnv, seed callbackConnectionSeed) (state, callbackURL string, connBefore *connection.Connection) {
	t.Helper()
	if seed == callbackSeedConnected {
		seedProviderLinkForGate(t, e.db, e.house, providerConnectGateID, connection.StateConnected, "gate-oauth-app")
	} else if seed == callbackSeedReauthRequired {
		seedProviderLinkForGate(t, e.db, e.house, providerConnectGateID, connection.StateReauthRequired, "gate-oauth-app")
	}
	state = e.mintAuthState()
	callbackURL = e.callbackURL(state, "good-code")
	connBefore = snapshotConnection(t, e.db, e.house, providerConnectGateID)
	return state, callbackURL, connBefore
}

func runOwnerGateTableKroger(t *testing.T, e *krogerConnectGateEnv, route ownerGateTableRoute, tc ownerGateTableCase, seed callbackConnectionSeed) {
	t.Helper()
	const provider = "kroger"
	householdReturn := route.kind == ownerGateRouteKrogerCallbackHousehold

	switch route.kind {
	case ownerGateRouteKrogerAuthorize:
		switch tc.kind {
		case ownerGateCaseMemberDenied:
			rec := e.member.authed(http.MethodGet, e.krogerAuthorizePath(), nil)
			assertForbiddenRole(t, rec)
			if got := krogerAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("member authorize minted kroger state %q", got)
			}
		case ownerGateCaseLookupError:
			rec := ownerGateRequest(t, e.providerConnectGateEnv, http.MethodGet, e.krogerAuthorizePath(), membershipErrHook())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("kroger authorize lookup error %d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "authorizationUrl") {
				t.Fatalf("lookup error succeeded: %s", rec.Body.String())
			}
			if got := krogerAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("lookup error minted kroger state %q", got)
			}
		case ownerGateCaseOwnerAllowed:
			rec := e.owner.authed(http.MethodGet, e.krogerAuthorizePath(), nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "authorizationUrl") {
				t.Fatalf("owner kroger authorize %d %s", rec.Code, rec.Body.String())
			}
		}
	case ownerGateRouteKrogerCallbackShopping, ownerGateRouteKrogerCallbackHousehold:
		state, callbackURL, connBefore := krogerCallbackFixture(t, e, householdReturn, seed)
		wantMemberLoc := "/shopping?kroger=not_allowed"
		wantOwnerFail := "/shopping?kroger=failed"
		wantOwnerOK := "/shopping?kroger=connected"
		if householdReturn {
			wantMemberLoc = "/household?kroger=not_allowed"
			wantOwnerOK = "/household?kroger=connected"
		}
		switch tc.kind {
		case ownerGateCaseMemberDenied:
			e.httpSpy.calls = 0
			rec := e.member.authed(http.MethodGet, callbackURL, nil)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("member kroger callback %d", rec.Code)
			}
			assertExactLocation(t, rec, wantMemberLoc)
			if got := krogerAuthState(t, e.db, e.house); got != "" {
				t.Fatalf("member callback left auth state %q", got)
			}
			if e.httpSpy.calls != 0 {
				t.Fatalf("member callback made %d HTTP calls", e.httpSpy.calls)
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if !connectionCredentialsUnchanged(connBefore, connAfter) {
				t.Fatalf("connection changed before=%+v after=%+v", connBefore, connAfter)
			}
			replay := e.owner.authed(http.MethodGet, callbackURL, nil)
			if replay.Code != http.StatusSeeOther {
				t.Fatalf("owner replay %d", replay.Code)
			}
			assertExactLocation(t, replay, wantOwnerFail)
		case ownerGateCaseLookupError:
			e.httpSpy.calls = 0
			rec := ownerGateRequest(t, e.providerConnectGateEnv, http.MethodGet, callbackURL, membershipErrHook())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("kroger callback lookup error %d %s", rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); strings.Contains(loc, "not_allowed") || strings.Contains(loc, "connected") {
				t.Fatalf("callback lookup error redirected %q", loc)
			}
			if got := krogerAuthState(t, e.db, e.house); got != state {
				t.Fatalf("callback lookup error changed auth state before=%q after=%q", state, got)
			}
			if e.httpSpy.calls != 0 {
				t.Fatalf("callback lookup error made %d HTTP calls", e.httpSpy.calls)
			}
			connAfter := snapshotConnection(t, e.db, e.house, provider)
			if !connectionCredentialsUnchanged(connBefore, connAfter) {
				t.Fatalf("connection changed before=%+v after=%+v", connBefore, connAfter)
			}
		case ownerGateCaseOwnerAllowed:
			e.httpSpy.calls = 0
			rec := e.owner.authed(http.MethodGet, callbackURL, nil)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("owner kroger callback %d %s", rec.Code, rec.Body.String())
			}
			assertExactLocation(t, rec, wantOwnerOK)
			if e.httpSpy.calls == 0 {
				t.Fatal("owner kroger callback made no HTTP calls")
			}
		}
	}
}

func krogerCallbackFixture(t *testing.T, e *krogerConnectGateEnv, householdReturn bool, seed callbackConnectionSeed) (state, callbackURL string, connBefore *connection.Connection) {
	t.Helper()
	const provider = "kroger"
	appKey := krogerHouseholdAppKey(t, e.db, e.house)
	if seed == callbackSeedConnected {
		seedProviderLinkForGate(t, e.db, e.house, provider, connection.StateConnected, appKey)
	} else if seed == callbackSeedReauthRequired {
		seedProviderLinkForGate(t, e.db, e.house, provider, connection.StateReauthRequired, appKey)
	}
	authPath := e.krogerAuthorizePath()
	if householdReturn {
		authPath += "?return=household"
	}
	// Owner starts connect (household return when this route requires it); member hits callback in the case body.
	rec := e.owner.authed(http.MethodGet, authPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner kroger authorize %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(body.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state = parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state in %q", body.AuthorizationURL)
	}
	callbackURL = e.krogerCallbackURL(state, "good-code")
	connBefore = snapshotConnection(t, e.db, e.house, "kroger")
	return state, callbackURL, connBefore
}

func ownerGateRequest(t *testing.T, e *providerConnectGateEnv, method, path string, hook func(context.Context, string, string) (string, string, bool, error)) *httptest.ResponseRecorder {
	t.Helper()
	req := stage1Request(method, path, e.owner.cookie, nil, app.EntryPublic, "")
	req.Header.Set("X-Household-Id", e.house)
	req = req.WithContext(auth.WithMembershipHook(req.Context(), hook))
	rec := httptest.NewRecorder()
	e.owner.h.ServeHTTP(rec, req)
	return rec
}

func runOwnerGateTableStores(t *testing.T, e *krogerConnectGateEnv, tc ownerGateTableCase) {
	t.Helper()
	const path = "/api/stores"
	switch tc.kind {
	case ownerGateCaseMemberDenied:
		rec := e.member.authed(http.MethodGet, path, nil)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"canConnect":true`) {
			t.Fatalf("member stores %d %s", rec.Code, rec.Body.String())
		}
	case ownerGateCaseLookupError:
		rec := ownerGateRequest(t, e.providerConnectGateEnv, http.MethodGet, path, membershipErrHook())
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("stores lookup error %d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"canConnect":true`) {
			t.Fatalf("stores lookup error returned canConnect true: %s", rec.Body.String())
		}
	case ownerGateCaseOwnerAllowed:
		rec := e.owner.authed(http.MethodGet, path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"canConnect":true`) {
			t.Fatalf("owner stores %d %s", rec.Code, rec.Body.String())
		}
	}
}

// TestProviderConnectOwnerGateCallbackMutantLocks names table rows that must fail mutants A/B/B2.
func TestProviderConnectOwnerGateCallbackMutantLocks(t *testing.T) {
	t.Run("mutant_A_callback_lookup_error", func(t *testing.T) {
		runOwnerGateTable(t, ownerGateTableRoute{name: "kroger_callback_shopping", kind: ownerGateRouteKrogerCallbackShopping},
			ownerGateTableCase{name: "lookup_error_fails_closed", kind: ownerGateCaseLookupError}, callbackSeedConnected)
	})
	t.Run("mutant_B_stores_lookup_error", func(t *testing.T) {
		runOwnerGateTable(t, ownerGateTableRoute{name: "stores_can_connect", kind: ownerGateRouteStoresCanConnect},
			ownerGateTableCase{name: "lookup_error_fails_closed", kind: ownerGateCaseLookupError}, callbackSeedNone)
	})
	t.Run("mutant_B2_household_not_allowed", func(t *testing.T) {
		runOwnerGateTable(t, ownerGateTableRoute{name: "kroger_callback_household", kind: ownerGateRouteKrogerCallbackHousehold},
			ownerGateTableCase{name: "member_denied", kind: ownerGateCaseMemberDenied}, callbackSeedConnected)
	})
	t.Run("mutant_D2_deny_disconnect", func(t *testing.T) {
		runOwnerGateTable(t, ownerGateTableRoute{name: "kroger_callback_household", kind: ownerGateRouteKrogerCallbackHousehold},
			ownerGateTableCase{name: "member_denied", kind: ownerGateCaseMemberDenied}, callbackSeedConnected)
	})
}
