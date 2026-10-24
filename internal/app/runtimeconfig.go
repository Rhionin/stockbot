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
// APP_BASE_URL is not filled in when the variable is empty: a process
// without it is LAN-only and does not open the public listener. The Pi
// deployment sets DefaultAppBaseURL. Renaming the site or the product is
// a change to these settings, not to call sites.
const (
	DefaultAppBaseURL        = "https://stockbot.rhionin.com"
	DefaultAppName           = "Stockbot"
	DefaultSessionCookieName = "__Host-stockbot_session"
	DefaultLANPeople         = LANPeopleRedirect
	DefaultExpoMinVersion    = "1.0.0"
	DefaultPublicListenAddr  = ":8081"
	DefaultLANListenAddr     = ":8080"
)

// How people on the LAN are treated after the household password is retired.
// redirect sends browsers to AppBaseURL. signin is the later LAN-cookie
// option and does not change Stage-1 behavior.
const (
	LANPeopleRedirect = "redirect"
	LANPeopleSignIn   = "signin"
)

// Entry is which listener accepted a request. The listener is the only
// signal: a LAN client cannot claim the public entry.
type Entry string

const (
	EntryLAN    Entry = "lan"
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
	return entry, ok && (entry == EntryLAN || entry == EntryPublic)
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
	LANPeople      string
	ExpoMinVersion string

	sessionCookieName     string
	publicListenAddr      string
	lanListenAddr         string
	publicListenerEnabled bool
}

// LoadRuntimeConfig reads the Stage-1 site settings. An empty APP_BASE_URL
// leaves the public listener unbound. A set APP_BASE_URL refuses to start
// when the public listener is turned off. getenv may be nil, which loads
// the defaults.
func LoadRuntimeConfig(getenv func(string) string) (RuntimeConfig, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg := RuntimeConfig{
		AppName:               DefaultAppName,
		LANPeople:             DefaultLANPeople,
		ExpoMinVersion:        DefaultExpoMinVersion,
		sessionCookieName:     DefaultSessionCookieName,
		publicListenAddr:      DefaultPublicListenAddr,
		lanListenAddr:         DefaultLANListenAddr,
		publicListenerEnabled: true,
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

	switch strings.ToLower(strings.TrimSpace(getenv("LAN_PEOPLE"))) {
	case "":
	case LANPeopleRedirect:
		cfg.LANPeople = LANPeopleRedirect
	case LANPeopleSignIn:
		cfg.LANPeople = LANPeopleSignIn
	default:
		return RuntimeConfig{}, errors.New("LAN_PEOPLE must be redirect or signin")
	}

	if raw := strings.TrimSpace(getenv("EXPO_MIN_VERSION")); raw != "" {
		if _, ok := parseSemver(raw); !ok {
			return RuntimeConfig{}, errors.New("EXPO_MIN_VERSION must be a version such as 1.0.0")
		}
		cfg.ExpoMinVersion = raw
	}

	lanRaw := strings.TrimSpace(getenv("LAN_LISTEN_ADDR"))
	if lanRaw == "" {
		lanRaw = strings.TrimSpace(getenv("ADDR"))
	}
	if lanRaw == "" {
		lanRaw = DefaultLANListenAddr
	}
	if listenerDisabled(lanRaw) || !validListenAddr(lanRaw) {
		return RuntimeConfig{}, errors.New("LAN_LISTEN_ADDR must be a host:port address such as :8080")
	}
	cfg.lanListenAddr = lanRaw

	if pubRaw := strings.TrimSpace(getenv("PUBLIC_LISTEN_ADDR")); pubRaw != "" {
		if listenerDisabled(pubRaw) {
			cfg.publicListenerEnabled = false
			cfg.publicListenAddr = ""
		} else if !validListenAddr(pubRaw) {
			return RuntimeConfig{}, errors.New("PUBLIC_LISTEN_ADDR must be a host:port address such as :8081, or off")
		} else {
			cfg.publicListenAddr = pubRaw
		}
	}

	if cfg.AppBaseURL != "" && !cfg.publicListenerEnabled {
		return RuntimeConfig{}, errors.New("APP_BASE_URL is set, but the public listener is off. Set PUBLIC_LISTEN_ADDR to an address such as :8081")
	}
	if cfg.publicListenerEnabled && cfg.AppBaseURL != "" && listenConflict(cfg.lanListenAddr, cfg.publicListenAddr) {
		return RuntimeConfig{}, errors.New("PUBLIC_LISTEN_ADDR and LAN_LISTEN_ADDR must be different addresses")
	}

	if err := cfg.checkPublicHost(getenv("PUBLIC_HOST")); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

// Listeners is the set of addresses to bind. The public listener is
// included only when APP_BASE_URL is set and the listener is enabled, so
// a LAN-only server does not need :8081.
func (c RuntimeConfig) Listeners() []Listener {
	out := []Listener{{Addr: c.lanListenAddr, Entry: EntryLAN}}
	if c.AppBaseURL != "" && c.publicListenerEnabled && c.publicListenAddr != "" {
		out = append(out, Listener{Addr: c.publicListenAddr, Entry: EntryPublic})
	}
	return out
}

// PublicListenerEnabled reports whether a public address may be bound.
// It stays true when PUBLIC_LISTEN_ADDR is unset. APP_BASE_URL still has
// to be set before Listeners includes that address.
func (c RuntimeConfig) PublicListenerEnabled() bool {
	return c.publicListenerEnabled
}

// PublicListenAddr is the configured public address, or empty when the
// public listener is turned off.
func (c RuntimeConfig) PublicListenAddr() string {
	return c.publicListenAddr
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

func listenConflict(a, b string) bool {
	ah, ap, aerr := net.SplitHostPort(a)
	bh, bp, berr := net.SplitHostPort(b)
	if aerr != nil || berr != nil || ap != bp {
		return false
	}
	return (unspecifiedHost(ah) || unspecifiedHost(bh)) || strings.EqualFold(ah, bh)
}

func unspecifiedHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::" || host == "[::]"
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
