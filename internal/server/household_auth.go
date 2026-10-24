package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/go-json-experiment/json"
	"golang.org/x/crypto/bcrypt"
)

const (
	// entryHeader is set by Caddy on the public hostname. The LAN listener
	// never sends it, so port 8080 stays open the way it is today.
	entryHeader = "X-Pantry-Entry"
	entryPublic = "public"
	// clientIPHeader is the visitor address Caddy observed. Caddy also
	// forwards CF-Connecting-IP and X-Forwarded-For, and header_up replaces
	// a client-supplied value on the way in.
	clientIPHeader = "X-Pantry-Client-IP"

	sessionCookie   = "pantry_session"
	sessionLifetime = 180 * 24 * time.Hour

	maxAuthFailures   = 5
	authFailureWindow = 15 * time.Minute
)

// HouseholdAuth checks the household password on the public hostname.
// The password is the bcrypt hash already stored for Caddy, so an existing
// install keeps the same username and passphrase.
type HouseholdAuth struct {
	Username   string
	hash       []byte
	secret     []byte
	Configured bool
	FailClosed bool
	now        func() time.Time
	limiter    *attemptLimiter
}

// WithHouseholdAuth installs the public-hostname login gate. A nil gate
// leaves every route open, which is the LAN and test default.
func WithHouseholdAuth(auth *HouseholdAuth) Option {
	return func(c *config) {
		c.household = auth
	}
}

// LoadHouseholdAuth reads a Caddy basic_auth file and the session secret.
// A missing or unreadable file returns a gate that refuses the public
// hostname instead of serving it openly. The LAN listener is unaffected.
// An empty session secret still allows sign-in; cookies from that process
// stop working when it exits, and the error says so.
func LoadHouseholdAuth(path, secret string) (*HouseholdAuth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return failClosed(), fmt.Errorf("read the household password file: %w", err)
	}
	user, hash, err := parseCaddyBasicAuth(data)
	if err != nil {
		return failClosed(), err
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return failClosed(), fmt.Errorf("the household password hash is not bcrypt")
	}
	gate := &HouseholdAuth{
		Username:   user,
		hash:       []byte(hash),
		Configured: true,
		limiter:    newAttemptLimiter(),
	}
	if key, ok := sessionKey(secret); ok {
		gate.secret = key
		return gate, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return failClosed(), fmt.Errorf("could not start household login")
	}
	gate.secret = buf
	return gate, fmt.Errorf("PANTRY_SESSION_SECRET is missing; sign-in works until this process restarts")
}

func failClosed() *HouseholdAuth {
	return &HouseholdAuth{FailClosed: true, limiter: newAttemptLimiter()}
}

// FailClosedHouseholdAuth refuses the public hostname. main uses it when
// PANTRY_AUTH_FILE is unset or empty, so a request marked public is never
// served without a password check. The LAN listener does not send that
// header and stays open.
func FailClosedHouseholdAuth() *HouseholdAuth {
	return failClosed()
}

func (a *HouseholdAuth) nowTime() time.Time {
	if a != nil && a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *HouseholdAuth) enforced(r *http.Request) bool {
	if a == nil || (!a.Configured && !a.FailClosed) {
		return false
	}
	// The listener is the entry. Stage 1 enforces the session on that
	// request. A request with no listener still uses X-Pantry-Entry, which
	// is how the household-password tests address the public site.
	if _, ok := app.EntryFrom(r.Context()); ok {
		return false
	}
	return r.Header.Get(entryHeader) == entryPublic
}

// legacyUsed reports whether the household-password routes are on this
// request. A listener entry replaces the X-Pantry-Entry header. The public
// listener still serves those routes while the shared password is on.
func (a *HouseholdAuth) legacyUsed(r *http.Request) bool {
	if a.enforced(r) {
		return true
	}
	entry, ok := app.EntryFrom(r.Context())
	return ok && entry == app.EntryPublic
}

type householdGateKey struct{}

func withHouseholdGate(ctx context.Context, g auth.HouseholdGate) context.Context {
	return context.WithValue(ctx, householdGateKey{}, g)
}

func householdGateFrom(ctx context.Context) (auth.HouseholdGate, bool) {
	if ctx == nil {
		return auth.HouseholdGate{}, false
	}
	g, ok := ctx.Value(householdGateKey{}).(auth.HouseholdGate)
	return g, ok
}

// Middleware lets the LAN through unchanged. On the public hostname it
// accepts a session cookie or a Basic credential, leaves the telemetry,
// build, brand, legal, deploy-hook, and sentry-tunnel paths open, and answers other API
// calls with JSON. Document loads continue to the web UI, which shows the
// login page. A public request is refused when the password file is not
// configured.
func (a *HouseholdAuth) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enforced(r) || isPublicPath(r) || syncAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !a.Configured {
			if isDocument(r) {
				next.ServeHTTP(w, r)
				return
			}
			// Session and login say the password file is missing. A 401 is
			// what a visitor who has not signed in also gets, so the rest
			// of the API stays that status and setup can still tell them apart.
			if (r.URL.Path == "/api/session" && r.Method == http.MethodGet) ||
				(r.URL.Path == "/api/login" && r.Method == http.MethodPost) {
				next.ServeHTTP(w, r)
				return
			}
			writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in required.")
			return
		}
		if isAuthEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}
		if user, pass, ok := basicCredentials(r); ok {
			key := clientKey(r)
			if blocked, retry := a.limiter.blocked(a.nowTime(), key); blocked {
				writeLimited(w, retry)
				return
			}
			if a.passwordMatches(user, pass) {
				a.limiter.reset(key)
				next.ServeHTTP(w, r)
				return
			}
			a.limiter.fail(a.nowTime(), key)
			writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in required.")
			return
		}
		// A session cookie is not a sign-in attempt. An address that is
		// paused can still use a browser that already signed in.
		if _, ok := a.sessionUser(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if isDocument(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in required.")
	})
}

// ServeSession tells the web UI whether this request must sign in.
func (a *HouseholdAuth) ServeSession(w http.ResponseWriter, r *http.Request) {
	if g, ok := householdGateFrom(r.Context()); ok && !g.On {
		writeJSON(w, http.StatusOK, map[string]any{"required": false})
		return
	}
	if !a.legacyUsed(r) {
		writeJSON(w, http.StatusOK, map[string]any{"required": false})
		return
	}
	if a == nil || !a.Configured {
		writeSignInNotConfigured(w)
		return
	}
	if user, pass, ok := basicCredentials(r); ok {
		key := clientKey(r)
		if blocked, retry := a.limiter.blocked(a.nowTime(), key); blocked {
			writeLimited(w, retry)
			return
		}
		if a.passwordMatches(user, pass) {
			a.limiter.reset(key)
			writeJSON(w, http.StatusOK, map[string]any{"required": true, "username": a.Username})
			return
		}
		a.limiter.fail(a.nowTime(), key)
		writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in required.")
		return
	}
	if user, ok := a.sessionUser(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"required": true, "username": user})
		return
	}
	writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "Sign in required.")
}

// ServeLogin checks the household password and sets the session cookie.
func (a *HouseholdAuth) ServeLogin(w http.ResponseWriter, r *http.Request) {
	if g, ok := householdGateFrom(r.Context()); ok && !g.On {
		writeAuthError(w, http.StatusNotFound, codeNotFound, "Sign-in is not used on this address.")
		return
	}
	if a == nil || !a.legacyUsed(r) {
		writeAuthError(w, http.StatusNotFound, codeNotFound, "Sign-in is not used on this address.")
		return
	}
	if !a.Configured {
		writeSignInNotConfigured(w)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeAuthError(w, http.StatusBadRequest, codeValidation, "Enter your username and password.")
		return
	}
	key := clientKey(r)
	if blocked, retry := a.limiter.blocked(a.nowTime(), key); blocked {
		writeLimited(w, retry)
		return
	}
	if !a.passwordMatchesRequest(r, body.Username, body.Password) {
		a.limiter.fail(a.nowTime(), key)
		writeAuthError(w, http.StatusUnauthorized, codeUnauthorized, "The username or password is incorrect.")
		return
	}
	a.limiter.reset(key)
	exp := a.nowTime().Add(sessionLifetime)
	userName := a.Username
	secret := a.secret
	if g, ok := householdGateFrom(r.Context()); ok && g.User != "" && len(g.Secret) > 0 {
		userName = g.User
		secret = g.Secret
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    signPayload(secret, userName, exp),
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": a.Username})
}

// ServeLogout clears the session cookie. It succeeds when the cookie is
// already gone so the menu can always sign out.
func (a *HouseholdAuth) ServeLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (a *HouseholdAuth) passwordMatchesRequest(r *http.Request, user, pass string) bool {
	if g, ok := householdGateFrom(r.Context()); ok && len(g.Hash) > 0 {
		sumUser := sha256.Sum256([]byte(user))
		sumWant := sha256.Sum256([]byte(g.User))
		userOK := subtle.ConstantTimeCompare(sumUser[:], sumWant[:]) == 1
		err := bcrypt.CompareHashAndPassword(g.Hash, []byte(pass))
		return userOK && err == nil
	}
	return a.passwordMatches(user, pass)
}

func (a *HouseholdAuth) passwordMatches(user, pass string) bool {
	sumUser := sha256.Sum256([]byte(user))
	sumWant := sha256.Sum256([]byte(a.Username))
	userOK := subtle.ConstantTimeCompare(sumUser[:], sumWant[:]) == 1
	err := bcrypt.CompareHashAndPassword(a.hash, []byte(pass))
	return userOK && err == nil
}

func (a *HouseholdAuth) sign(user string, exp time.Time) string {
	return signPayload(a.secret, user, exp)
}

func signPayload(secret []byte, user string, exp time.Time) string {
	payload := []byte(user + "|" + strconv.FormatInt(exp.Unix(), 10))
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func pantryCookieOK(secret []byte, username string, now time.Time, raw string) bool {
	if len(secret) == 0 || username == "" || raw == "" {
		return false
	}
	payloadB64, macB64, ok := strings.Cut(raw, ".")
	if !ok {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return false
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok || user != username {
		return false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(expUnix, 0))
}

func (a *HouseholdAuth) sessionUser(r *http.Request) (string, bool) {
	if a == nil || !a.Configured || len(a.secret) == 0 {
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	payloadB64, macB64, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", false
	}
	got, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return "", false
	}
	user, expStr, ok := strings.Cut(string(payload), "|")
	if !ok || user != a.Username {
		return "", false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if !a.nowTime().Before(time.Unix(expUnix, 0)) {
		return "", false
	}
	return user, true
}

// cookieSecure is true on the public hostname even when Caddy's connection
// to Pantry is plain HTTP. Tunnel mode terminates TLS at Cloudflare, so
// X-Forwarded-Proto can say http while the browser is on https.
func cookieSecure(r *http.Request) bool {
	if r.Header.Get(entryHeader) == entryPublic {
		return true
	}
	return requestHTTPS(r)
}

func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.Index(proto, ","); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorPayload{Error: message, Code: code})
}

func writeSignInNotConfigured(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, errorPayload{
		Error: clientmsg.AuthNotConfigured,
		Code:  authNotConfiguredCode,
	})
}

func writeLimited(w http.ResponseWriter, retry time.Duration) {
	secs := int(retry.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeAuthError(w, http.StatusTooManyRequests, codeRateLimited, "Too many sign-in attempts. Try again later.")
}

func isAuthEndpoint(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/login", "/api/logout":
		return r.Method == http.MethodPost
	case "/api/session":
		return r.Method == http.MethodGet
	default:
		return false
	}
}

func isDocument(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/health" || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	return true
}

// isPublicPath matches the paths Caddy serves without a password. The Kroger
// callback is not one of them: a SameSite=Lax cookie is sent on the top-level
// GET that brings the browser back from Kroger.
func isPublicPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/telemetry", "/api/telemetry/client", "/api/build", "/api/deploy-hook", sentryTunnelPath, "/brand/logo.png", "/terms", "/privacy":
		return true
	default:
		return false
	}
}

func parseCaddyBasicAuth(data []byte) (string, string, error) {
	var user, hash string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "basic_auth") || line == "{" || line == "}" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if !strings.HasPrefix(fields[1], "$2a$") && !strings.HasPrefix(fields[1], "$2b$") && !strings.HasPrefix(fields[1], "$2y$") {
			continue
		}
		user, hash = fields[0], fields[1]
		break
	}
	if user == "" || hash == "" {
		return "", "", fmt.Errorf("the household password file has no bcrypt hash")
	}
	if !validHouseholdUser(user) {
		return "", "", fmt.Errorf("the household password file has an invalid username")
	}
	return user, hash, nil
}

func validHouseholdUser(user string) bool {
	if user == "" || len(user) > 64 {
		return false
	}
	for i, c := range user {
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		digit := c >= '0' && c <= '9'
		extra := c == '.' || c == '_' || c == '-'
		if i == 0 && !letter {
			return false
		}
		if !letter && !digit && !extra {
			return false
		}
	}
	return true
}

func sessionKey(secret string) ([]byte, bool) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 32 {
		return nil, false
	}
	return []byte(secret), true
}

func basicCredentials(r *http.Request) (string, string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "basic "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(prefix):]))
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(strings.TrimSpace(h[len(prefix):]))
		if err != nil {
			return "", "", false
		}
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", false
	}
	return user, pass, true
}

func clientKey(r *http.Request) string {
	ip := visitorIP(r)
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip = host
	}
	return "ip:" + ip
}

// visitorIP is the client address Caddy forwarded. CF-Connecting-IP is the
// Cloudflare visitor. X-Forwarded-For is the first address in that list.
// X-Pantry-Client-IP remains for a proxy that still sets it. A value that
// is not one IP is ignored so a forged list cannot rotate the rate limit.
func visitorIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := oneIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if ip := oneIP(firstForwarded(r.Header.Get("X-Forwarded-For"))); ip != "" {
		return ip
	}
	return oneIP(r.Header.Get(clientIPHeader))
}

func firstForwarded(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	first, _, _ := strings.Cut(raw, ",")
	return strings.TrimSpace(first)
}

func oneIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t,") || net.ParseIP(raw) == nil {
		return ""
	}
	return raw
}

type attemptLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newAttemptLimiter() *attemptLimiter {
	return &attemptLimiter{hits: map[string][]time.Time{}}
}

func (l *attemptLimiter) blocked(now time.Time, keys ...string) (bool, time.Duration) {
	if l == nil {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var retry time.Duration
	blocked := false
	for _, key := range keys {
		l.prune(now, key)
		times := l.hits[key]
		if len(times) >= maxAuthFailures {
			blocked = true
			until := times[0].Add(authFailureWindow).Sub(now)
			if until > retry {
				retry = until
			}
		}
	}
	if blocked && retry < time.Second {
		retry = time.Second
	}
	return blocked, retry
}

func (l *attemptLimiter) fail(now time.Time, keys ...string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		l.prune(now, key)
		l.hits[key] = append(l.hits[key], now)
	}
}

func (l *attemptLimiter) reset(keys ...string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.hits, key)
	}
}

func (l *attemptLimiter) prune(now time.Time, key string) {
	times := l.hits[key]
	kept := times[:0]
	for _, ts := range times {
		if now.Sub(ts) < authFailureWindow {
			kept = append(kept, ts)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return
	}
	l.hits[key] = kept
}
