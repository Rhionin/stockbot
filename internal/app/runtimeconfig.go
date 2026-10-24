package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// These are the Stage-1 defaults from the multi-household design (§10).
// APP_BASE_URL is not filled in when the variable is empty. The process
// still listens once, and that listener requires a session. The Pi
// deployment sets DefaultAppBaseURL. Renaming the site or the product is
// a change to these settings, not to call sites.
const (
	DefaultAppBaseURL        = "https://stockbot.rhionin.com"
	DefaultAppName           = "Stockbot"
	DefaultSessionCookieName = "__Host-stockbot_session"
	DefaultExpoMinVersion    = "1.0.0"
	DefaultListenAddr        = ":8080"
)

// Entry is the authenticated site. The process binds one listener, and
// every request on it is this entry.
type Entry string

const (
	EntryPublic Entry = "public"
)

type entryKey struct{}

// WithEntry records the listener that accepted r.
func WithEntry(ctx context.Context, entry Entry) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, entryKey{}, entry)
}

// EntryFrom reports the listener that accepted the request.
// The second result is false when the request did not come through a
// configured listener.
func EntryFrom(ctx context.Context) (Entry, bool) {
	if ctx == nil {
		return "", false
	}
	entry, ok := ctx.Value(entryKey{}).(Entry)
	return entry, ok && entry == EntryPublic
}

// Listener is one address the process binds, and the entry it represents.
type Listener struct {
	Addr  string
	Entry Entry
}

// SessionCookie is the personal-session cookie described in design §3.5.
// Domain is empty for a host-only cookie. Secure is always set: a later
// HTTPS listener can use the same cookie without a code change. The
// __Host- prefix is omitted when Domain is set, because that prefix
// forbids a Domain attribute.
type SessionCookie struct {
	Name     string
	Domain   string
	Path     string
	Secure   bool
	HttpOnly bool
	SameSite http.SameSite
}

// RuntimeConfig is the site identity and the listeners for one process.
type RuntimeConfig struct {
	AppBaseURL     string
	AppName        string
	PublicHost     string
	CookieDomain   string
	EmailFrom      string
	ExpoMinVersion string

	sessionCookieName string
	listenAddr        string
}

// LoadRuntimeConfig reads the Stage-1 site settings. The process binds
// one listener. ADDR is that address, defaulting to :8080. An old
// LAN_LISTEN_ADDR is used only when ADDR is empty. PUBLIC_LISTEN_ADDR
// and LAN_PEOPLE are ignored, so a previous Pi env does not open a
// second listener. getenv may be nil, which loads the defaults.
func LoadRuntimeConfig(getenv func(string) string) (RuntimeConfig, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := RuntimeConfig{
		AppName:           DefaultAppName,
		ExpoMinVersion:    DefaultExpoMinVersion,
		sessionCookieName: DefaultSessionCookieName,
		listenAddr:        DefaultListenAddr,
	}

	origin, host, err := normalizeBaseURL(getenv("APP_BASE_URL"))
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.AppBaseURL = origin
	cfg.PublicHost = host

	if name := strings.TrimSpace(getenv("APP_NAME")); name != "" {
		if !validAppName(name) {
			return RuntimeConfig{}, errors.New("APP_NAME must be a short name with no line breaks")
		}
		cfg.AppName = name
	}

	if raw := strings.TrimSpace(getenv("SESSION_COOKIE_NAME")); raw != "" {
		if !validCookieName(raw) {
			return RuntimeConfig{}, errors.New("SESSION_COOKIE_NAME must be one cookie token")
		}
		cfg.sessionCookieName = raw
	}

	if raw := strings.TrimSpace(getenv("COOKIE_DOMAIN")); raw != "" {
		domain, ok := validCookieDomain(raw)
		if !ok {
			return RuntimeConfig{}, errors.New("COOKIE_DOMAIN must be a hostname, with no scheme or port")
		}
		cfg.CookieDomain = domain
	}
	if cfg.CookieDomain != "" && cfg.PublicHost != "" && !domainCovers(cfg.CookieDomain, cfg.PublicHost) {
		return RuntimeConfig{}, errors.New("COOKIE_DOMAIN must be the public host or a parent of it")
	}
	if _, err := cfg.cookieName(); err != nil {
		return RuntimeConfig{}, err
	}

	if raw := strings.TrimSpace(getenv("EMAIL_FROM")); raw != "" {
		if !validEmail(raw) {
			return RuntimeConfig{}, errors.New("EMAIL_FROM must be an email address")
		}
		cfg.EmailFrom = raw
	}

	if raw := strings.TrimSpace(getenv("EXPO_MIN_VERSION")); raw != "" {
		if _, ok := parseSemver(raw); !ok {
			return RuntimeConfig{}, errors.New("EXPO_MIN_VERSION must be a version such as 1.0.0")
		}
		cfg.ExpoMinVersion = raw
	}

	addr, err := configuredListenAddr(getenv)
	if err != nil {
		return RuntimeConfig{}, err
	}
	cfg.listenAddr = addr

	if err := cfg.checkPublicHost(getenv("PUBLIC_HOST")); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

// configuredListenAddr is ADDR, or LAN_LISTEN_ADDR when ADDR is empty.
// PUBLIC_LISTEN_ADDR is not a second bind.
func configuredListenAddr(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv("ADDR"))
	if raw == "" || listenerDisabled(raw) {
		raw = strings.TrimSpace(getenv("LAN_LISTEN_ADDR"))
	}
	if raw == "" || listenerDisabled(raw) {
		return DefaultListenAddr, nil
	}
	if !validListenAddr(raw) {
		return "", errors.New("ADDR must be a host:port address such as :8080")
	}
	return raw, nil
}

// Listeners is the one address to bind. Every request on it is the
// public entry, so a session is required for the app and the API.
func (c RuntimeConfig) Listeners() []Listener {
	addr := c.listenAddr
	if addr == "" {
		addr = DefaultListenAddr
	}
	return []Listener{{Addr: addr, Entry: EntryPublic}}
}

// SessionCookie is the cookie to set for a personal session.
func (c RuntimeConfig) SessionCookie() SessionCookie {
	name, _ := c.cookieName()
	return SessionCookie{
		Name:     name,
		Domain:   c.CookieDomain,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// AbsoluteURL joins path onto the public origin. An empty APP_BASE_URL
// returns an empty string so a link is not built with a stand-in host.
func (c RuntimeConfig) AbsoluteURL(path string) string {
	if c.AppBaseURL == "" {
		return ""
	}
	if path == "" {
		return c.AppBaseURL
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.AppBaseURL + path
}

// AllowsOrigin reports whether origin is exactly APP_BASE_URL.
// Cookie requests that change state use this check. An empty APP_BASE_URL
// allows nothing, because there is no public origin to match.
func (c RuntimeConfig) AllowsOrigin(origin string) bool {
	if c.AppBaseURL == "" {
		return false
	}
	got, _, err := normalizeBaseURL(origin)
	return err == nil && got == c.AppBaseURL
}

// ExpoUpgradeRequired reports whether an Expo client header is below
// EXPO_MIN_VERSION. A missing header, or any value that does not start
// with expo/, is not Expo and is not gated. expo/ followed by something
// other than a version is gated.
func (c RuntimeConfig) ExpoUpgradeRequired(clientHeader string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(clientHeader), "expo/")
	if !ok {
		return false
	}
	got, ok := parseSemver(rest)
	if !ok {
		return true
	}
	min, ok := parseSemver(c.ExpoMinVersion)
	if !ok {
		return false
	}
	return compareSemver(got, min) < 0
}

func (c RuntimeConfig) cookieName() (string, error) {
	name := c.sessionCookieName
	if c.CookieDomain != "" && strings.HasPrefix(name, "__Host-") {
		name = strings.TrimPrefix(name, "__Host-")
	}
	if name == "" || strings.HasPrefix(name, "__Host-") && c.CookieDomain != "" {
		return "", errors.New("SESSION_COOKIE_NAME needs a name when COOKIE_DOMAIN is set. The __Host- prefix cannot be used with a domain")
	}
	if !validCookieName(name) {
		return "", errors.New("SESSION_COOKIE_NAME must be one cookie token")
	}
	return name, nil
}

func (c RuntimeConfig) checkPublicHost(raw string) error {
	if c.PublicHost == "" || strings.TrimSpace(raw) == "" {
		return nil
	}
	got := canonicalHost(raw)
	if got == "" || got != c.PublicHost {
		return errors.New("PUBLIC_HOST must match the host in APP_BASE_URL")
	}
	return nil
}

func normalizeBaseURL(raw string) (origin, host string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", errors.New("APP_BASE_URL must be an http or https address with no path, query, or user")
	}
	if parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", "", errors.New("APP_BASE_URL must be an http or https address with no path, query, or user")
	}
	host = strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return "", "", errors.New("APP_BASE_URL must be an http or https address with no path, query, or user")
	}
	originHost := host
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", errors.New("APP_BASE_URL must be an http or https address with no path, query, or user")
		}
		if !defaultPort(parsed.Scheme, port) {
			originHost = net.JoinHostPort(host, port)
		}
	}
	return parsed.Scheme + "://" + originHost, host, nil
}

func defaultPort(scheme, port string) bool {
	return scheme == "https" && port == "443" || scheme == "http" && port == "80"
}

func canonicalHost(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" || strings.ContainsAny(raw, "/?#") {
		return ""
	}
	parsed, err := url.Parse("//" + raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(parsed.Hostname(), ".")
}

func validAppName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return false
	}
	return !strings.ContainsAny(name, "\r\n")
}

func validCookieName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
			continue
		default:
			return false
		}
	}
	return true
}

func validCookieDomain(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.TrimPrefix(raw, ".")
	if raw == "" || strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@") || net.ParseIP(raw) != nil {
		return "", false
	}
	labels := strings.Split(raw, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				continue
			}
			return "", false
		}
	}
	return raw, true
}

func domainCovers(domain, host string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func validEmail(raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	local, host, ok := strings.Cut(raw, "@")
	if !ok || local == "" || strings.Contains(local, "@") {
		return false
	}
	_, ok = validCookieDomain(host)
	return ok
}

func listenerDisabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "disabled", "none", "false", "-":
		return true
	default:
		return false
	}
}

func validListenAddr(raw string) bool {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || port == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return false
	}
	if host == "" || net.ParseIP(host) != nil {
		return true
	}
	_, ok := validCookieDomain(host)
	return ok
}

type semver struct {
	major, minor, patch int
}

func parseSemver(raw string) (semver, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return semver{}, false
	}
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, false
	}
	nums := [3]int{}
	for i, part := range parts {
		if part == "" || len(part) > 9 {
			return semver{}, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2]}, true
}

func compareSemver(a, b semver) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return a.patch - b.patch
}
