package server

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

const (
	householdHeader = "X-Household-Id"
	clientHeader    = "X-Stockbot-Client"
	deviceScanLimit = 64 * 1024
)

type stage1 struct {
	svc      *auth.Service
	site     app.RuntimeConfig
	stockIn  string
	stockOut string
}

func (s *stage1) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry, hasEntry := app.EntryFrom(r.Context())
		if !hasEntry {
			s.attachPrimary(r)
			next.ServeHTTP(w, r)
			return
		}
		if s.upgradeRequired(w, r) {
			return
		}
		if r.URL.Path == "/setup" && r.Method == http.MethodPost {
			s.serveSetupPost(w, r)
			return
		}
		if r.URL.Path == "/setup" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			s.serveSetupPage(w, r, next)
			return
		}
		if entry == app.EntryLAN && s.blockLAN(w, r) {
			return
		}
		if gate, err := s.svc.LoadHouseholdGate(r.Context()); err == nil {
			r = r.WithContext(withHouseholdGate(r.Context(), gate))
		}
		if syncAuthorized(r) || s.anonymousOpen(r) {
			s.attachPrimary(r)
			next.ServeHTTP(w, r)
			return
		}
		principal, ok := s.resolve(w, r, entry)
		if !ok {
			return
		}
		if principal.FromCookie && !s.csrfOK(r) {
			writeStage1(w, auth.AsError(unauthorizedSentinel()))
			return
		}
		principal, ok = s.bindHousehold(w, r, principal)
		if !ok {
			return
		}
		ctx := auth.WithPrincipal(r.Context(), principal)
		if principal.HouseholdID != "" {
			if scope, err := tenant.New(principal.HouseholdID); err == nil {
				ctx = tenant.WithScope(ctx, scope)
			}
		} else {
			if scope, err := s.svc.Primary(r.Context()); err == nil {
				ctx = tenant.WithScope(ctx, scope)
			}
		}
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func unauthorizedSentinel() error {
	return &auth.Error{Code: auth.CodeUnauthorized, Message: auth.SentenceUnauthorized}
}

func (s *stage1) upgradeRequired(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/config", "/api/build", "/api/telemetry", "/api/telemetry/client":
		return false
	}
	if s.site.ExpoUpgradeRequired(r.Header.Get(clientHeader)) {
		writeStage1(w, &auth.Error{Code: auth.CodeUpgrade, Message: auth.SentenceUpgrade})
		return true
	}
	return false
}

func (s *stage1) blockLAN(w http.ResponseWriter, r *http.Request) bool {
	if token := bearerToken(r); strings.HasPrefix(token, "sbs_") {
		writeStage1(w, unauthorizedSentinel())
		return true
	}
	if lanRouteMissing(r) {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return true
	}
	open, err := s.svc.LANOpen(r.Context())
	if err != nil {
		writeStage1(w, err)
		return true
	}
	if open {
		return false
	}
	if isDocument(r) {
		if s.site.AppBaseURL != "" {
			http.Redirect(w, r, s.site.AppBaseURL, http.StatusFound)
			return true
		}
		return false
	}
	if lanStaysOpen(r) {
		return false
	}
	if r.URL.Path == "/api/auth/session" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeStage1(w, unauthorizedSentinel())
		return true
	}
	return false
}

func lanRouteMissing(r *http.Request) bool {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/reset":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/households":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/invites/accept" && acceptCarriesAccount(r):
		return true
	default:
		return false
	}
}

func acceptCarriesAccount(r *http.Request) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return bytes.Contains(body, []byte(`"account"`))
}

func lanStaysOpen(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/config", "/api/build", "/health", "/api/telemetry/client", "/api/devices/pair/complete":
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/device/")
}

func (s *stage1) anonymousOpen(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/config", "/api/build", "/health", "/api/telemetry/client",
		"/api/deploy-hook", "/brand/logo.png", "/terms", "/privacy":
		return true
	case "/api/telemetry":
		entry, _ := app.EntryFrom(r.Context())
		if entry != app.EntryLAN {
			return true
		}
		open, err := s.svc.LANOpen(r.Context())
		return err == nil && open
	}
	if r.URL.Path == sentryTunnelPath {
		return true
	}
	return false
}

func (s *stage1) resolve(w http.ResponseWriter, r *http.Request, entry app.Entry) (auth.Principal, bool) {
	if retiredBasic(r, entry) {
		on, err := s.svc.HouseholdPasswordOn(r.Context())
		if err != nil {
			writeStage1(w, err)
			return auth.Principal{}, false
		}
		if !on && !strings.HasPrefix(r.Header.Get(clientHeader), "expo/") {
			writeStage1(w, &auth.Error{Code: auth.CodeUpgrade, Message: auth.SentenceUpgrade})
			return auth.Principal{}, false
		}
	}
	token := bearerToken(r)
	if strings.HasPrefix(token, "sbd_") {
		if !strings.HasPrefix(r.URL.Path, "/api/device/") {
			writeStage1(w, unauthorizedSentinel())
			return auth.Principal{}, false
		}
		p, err := s.svc.AuthenticateDevice(r.Context(), token)
		if err != nil {
			writeStage1(w, err)
			return auth.Principal{}, false
		}
		return p, true
	}
	if strings.HasPrefix(token, "sbs_") {
		if strings.HasPrefix(r.URL.Path, "/api/device/") {
			writeStage1(w, unauthorizedSentinel())
			return auth.Principal{}, false
		}
		p, _, err := s.svc.LookupSession(r.Context(), token)
		if err != nil {
			writeStage1(w, err)
			return auth.Principal{}, false
		}
		return p, true
	}
	if entry != app.EntryLAN {
		if raw := sessionCookieValue(r, s.site); raw != "" {
			p, _, err := s.svc.LookupSession(r.Context(), raw)
			if err != nil {
				writeStage1(w, err)
				return auth.Principal{}, false
			}
			p.FromCookie = true
			return p, true
		}
	}
	if entry == app.EntryLAN {
		open, err := s.svc.LANOpen(r.Context())
		if err != nil {
			writeStage1(w, err)
			return auth.Principal{}, false
		}
		if open {
			p, _, err := s.svc.LANView(r.Context())
			if err != nil {
				writeStage1(w, err)
				return auth.Principal{}, false
			}
			return p, true
		}
	}
	if entry == app.EntryPublic {
		if p, ok := s.householdPrincipal(r); ok {
			return p, true
		}
	}
	if !requiresPrincipal(r) {
		return auth.Principal{}, true
	}
	writeStage1(w, unauthorizedSentinel())
	return auth.Principal{}, false
}

func retiredBasic(r *http.Request, entry app.Entry) bool {
	if entry != app.EntryPublic {
		return false
	}
	_, _, ok := basicCredentials(r)
	return ok
}

func (s *stage1) householdPrincipal(r *http.Request) (auth.Principal, bool) {
	gate, ok := householdGateFrom(r.Context())
	if !ok || !gate.On {
		return auth.Principal{}, false
	}
	if user, pass, ok := basicCredentials(r); ok {
		match, err := s.svc.CheckHouseholdPassword(r.Context(), user, pass)
		if err != nil || !match {
			return auth.Principal{}, false
		}
		scope, err := s.svc.Primary(r.Context())
		if err != nil {
			return auth.Principal{}, false
		}
		return auth.Principal{Kind: "household_password", HouseholdID: scope.HouseholdID(), Role: "member"}, true
	}
	if c, err := r.Cookie(sessionCookie); err == nil && pantryCookieOK(gate.Secret, gate.User, s.svc.Now(), c.Value) {
		scope, err := s.svc.Primary(r.Context())
		if err != nil {
			return auth.Principal{}, false
		}
		return auth.Principal{Kind: "household_password", HouseholdID: scope.HouseholdID(), Role: "member", FromCookie: true}, true
	}
	return auth.Principal{}, false
}

func sessionCookieValue(r *http.Request, site app.RuntimeConfig) string {
	c, err := r.Cookie(site.SessionCookie().Name)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *stage1) csrfOK(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if bearerToken(r) != "" {
		return true
	}
	switch r.URL.Path {
	case "/api/auth/login", "/api/auth/reset", "/api/invites/accept", "/setup", "/api/login":
		return true
	}
	return s.site.AllowsOrigin(r.Header.Get("Origin"))
}

func (s *stage1) bindHousehold(w http.ResponseWriter, r *http.Request, p auth.Principal) (auth.Principal, bool) {
	if p.Kind == "lan" || p.Kind == "household_password" {
		scope, err := s.svc.Primary(r.Context())
		if err != nil {
			writeStage1(w, err)
			return p, false
		}
		p.HouseholdID = scope.HouseholdID()
		p.Role = "member"
		return p, true
	}
	if p.Kind == "device" {
		header := strings.TrimSpace(r.Header.Get(householdHeader))
		if header != "" && canonicalID(header) != p.HouseholdID {
			writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
			return p, false
		}
		return p, true
	}
	if p.Kind != "account" || !needsHousehold(r) {
		return p, true
	}
	selected := ""
	if header := strings.TrimSpace(r.Header.Get(householdHeader)); header != "" {
		selected = canonicalID(header)
		if selected == "" {
			writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
			return p, false
		}
	} else if p.HouseholdID != "" {
		selected = p.HouseholdID
	} else {
		writeStage1(w, &auth.Error{Code: auth.CodeValidation, Message: auth.SentenceValidation, Field: householdHeader})
		return p, false
	}
	role, deleted, ok, err := s.svc.Membership(r.Context(), p.UserID, selected)
	if err != nil {
		writeStage1(w, err)
		return p, false
	}
	if !ok {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return p, false
	}
	if pathID := pathHousehold(r.URL.Path); pathID != "" && canonicalID(pathID) != selected {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return p, false
	}
	if deleted != "" && !allowsDeleted(r) {
		writeStage1(w, &auth.Error{Code: auth.CodeNotFound, Message: auth.SentenceNotFound})
		return p, false
	}
	p.HouseholdID = selected
	p.Role = role
	return p, true
}

func (s *stage1) attachPrimary(r *http.Request) {
	if _, ok := tenant.FromContext(r.Context()); ok {
		return
	}
	scope, err := s.svc.Primary(r.Context())
	if err != nil {
		return
	}
	cloned := r.WithContext(tenant.WithScope(r.Context(), scope))
	*r = *cloned
}

func canonicalID(id string) string {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return ""
	}
	return parsed.String()
}

func pathHousehold(path string) string {
	rest, ok := strings.CutPrefix(path, "/api/households/")
	if !ok || rest == "" {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	return id
}

func needsHousehold(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	switch path {
	case "/api/auth/session", "/api/auth/login", "/api/auth/logout", "/api/auth/reset",
		"/api/auth/session/household", "/api/invites/preview", "/api/invites/accept",
		"/api/devices/pair/complete", "/api/operator/household-invites",
		"/api/households", "/api/config", "/api/build", "/api/telemetry",
		"/api/telemetry/client", "/api/me", "/api/session", "/api/login", "/api/logout":
		return false
	}
	if strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/device/") {
		return false
	}
	return true
}

func allowsDeleted(r *http.Request) bool {
	path := r.URL.Path
	if path == "/api/households" && r.Method == http.MethodGet {
		return true
	}
	if path == "/api/auth/session" || path == "/api/auth/logout" || path == "/api/auth/session/household" {
		return true
	}
	if strings.HasPrefix(path, "/api/households/") && !strings.Contains(strings.TrimPrefix(path, "/api/households/"), "/") && r.Method == http.MethodGet {
		return true
	}
	return strings.HasSuffix(path, "/restore") && r.Method == http.MethodPost
}

func requiresPrincipal(r *http.Request) bool {
	if isDocument(r) || sOpen(r) {
		return false
	}
	switch r.URL.Path {
	case "/api/auth/login", "/api/auth/callback", "/api/auth/reset", "/api/invites/preview", "/api/invites/accept",
		"/api/devices/pair/complete", "/setup", "/api/auth/session", "/api/config":
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	return false
}

func sOpen(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/build", "/health", "/api/telemetry", "/api/telemetry/client", "/api/deploy-hook",
		"/brand/logo.png", "/terms", "/privacy":
		return true
	}
	return r.URL.Path == sentryTunnelPath
}

func eventStreamKey(r *http.Request) string {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		return ""
	}
	switch p.Kind {
	case "account":
		return auth.UserStreamKey(p.UserID)
	case "household_password":
		return auth.HouseholdPasswordStreamKey()
	case "lan":
		return "lan:" + stage1ClientIP(r)
	default:
		return ""
	}
}

func stage1ClientIP(r *http.Request) string {
	entry, _ := app.EntryFrom(r.Context())
	if entry == app.EntryPublic {
		ip := strings.TrimSpace(r.Header.Get(clientIPHeader))
		if ip != "" && !strings.ContainsAny(ip, " \t,") {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeStage1(w http.ResponseWriter, err error) {
	var ae *auth.Error
	if !errorsAs(err, &ae) {
		writeStatusError(nil, w, http.StatusInternalServerError, err)
		return
	}
	status := stage1Status(ae.Code)
	if ae.Code == auth.CodeRateLimited {
		secs := int(ae.Retry.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	writeJSON(w, status, struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Field string `json:"field,omitempty"`
	}{Error: ae.Message, Code: ae.Code, Field: ae.Field})
}

func stage1Status(code string) int {
	switch code {
	case auth.CodeUnauthorized, auth.CodeDeviceRevoked:
		return http.StatusUnauthorized
	case auth.CodeForbiddenRole, auth.CodeInviteRequired:
		return http.StatusForbidden
	case auth.CodeNotFound:
		return http.StatusNotFound
	case auth.CodeValidation, auth.CodeConfirm:
		return http.StatusBadRequest
	case auth.CodeRateLimited:
		return http.StatusTooManyRequests
	case auth.CodeBusy, auth.CodeNoSpace:
		return http.StatusServiceUnavailable
	case auth.CodeInviteExpired, auth.CodeResetExpired, auth.CodeAccountExists, auth.CodeLastOwner, auth.CodeConflict:
		return http.StatusConflict
	case auth.CodeUpgrade:
		return http.StatusUpgradeRequired
	case auth.CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		if code == "unwritable" {
			return http.StatusNotFound
		}
		return http.StatusInternalServerError
	}
}

func errorsAs(err error, target **auth.Error) bool {
	return errors.As(err, target)
}

func (s *stage1) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	entry, _ := app.EntryFrom(r.Context())
	if entry == app.EntryLAN || token == "" {
		return
	}
	ck := s.site.SessionCookie()
	http.SetCookie(w, &http.Cookie{
		Name: ck.Name, Value: token, Path: ck.Path, Domain: ck.Domain,
		Secure: ck.Secure, HttpOnly: ck.HttpOnly, SameSite: ck.SameSite,
		MaxAge: int((180 * 24 * time.Hour).Seconds()),
	})
}

func (s *stage1) clearCookie(w http.ResponseWriter) {
	ck := s.site.SessionCookie()
	http.SetCookie(w, &http.Cookie{
		Name: ck.Name, Value: "", Path: ck.Path, Domain: ck.Domain,
		Secure: ck.Secure, HttpOnly: ck.HttpOnly, SameSite: ck.SameSite,
		MaxAge: -1, Expires: time.Unix(0, 0),
	})
}

func expoClient(r *http.Request) bool {
	return strings.HasPrefix(strings.TrimSpace(r.Header.Get(clientHeader)), "expo/")
}

func presentedToken(r *http.Request, site app.RuntimeConfig) string {
	if token := bearerToken(r); strings.HasPrefix(token, "sbs_") {
		return token
	}
	return sessionCookieValue(r, site)
}
