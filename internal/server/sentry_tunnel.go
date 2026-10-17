package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	sentryTunnelPath        = "/api/sentry-tunnel"
	sentryTunnelMaxBody     = 256 * 1024
	sentryTunnelPerIP       = 30
	sentryTunnelGlobal      = 120
	sentryTunnelWindow      = time.Minute
	sentryTunnelTimeout     = 5 * time.Second
	sentryEnvelopeMediaType = "application/x-sentry-envelope"
)

var projectIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

var errInvalidEnvelope = errors.New("invalid envelope")

// SentryTunnelConfig is the allowlist for POST /api/sentry-tunnel.
// An empty host or an empty project list leaves the route hidden.
// PublicKeys, when non-empty, must contain the envelope DSN's public key.
type SentryTunnelConfig struct {
	IngestHost string
	ProjectIDs map[string]struct{}
	PublicKeys map[string]struct{}
	Client     *http.Client
	PerIP      int
	Global     int
	Window     time.Duration
	MaxBody    int64
}

type sentryTunnel struct {
	host       string
	projects   map[string]struct{}
	publicKeys map[string]struct{}
	client     *http.Client
	limiter    *tunnelLimiter
	maxBody    int64
}

func newSentryTunnel(cfg SentryTunnelConfig) http.Handler {
	host := strings.ToLower(strings.TrimSpace(cfg.IngestHost))
	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = sentryTunnelMaxBody
	}
	client := cfg.Client
	if client == nil {
		client = newTunnelHTTPClient()
	}
	perIP := cfg.PerIP
	if perIP <= 0 {
		perIP = sentryTunnelPerIP
	}
	global := cfg.Global
	if global <= 0 {
		global = sentryTunnelGlobal
	}
	window := cfg.Window
	if window <= 0 {
		window = sentryTunnelWindow
	}
	return &sentryTunnel{
		host:       host,
		projects:   cfg.ProjectIDs,
		publicKeys: cfg.PublicKeys,
		client:     client,
		limiter:    newTunnelLimiter(perIP, global, window),
		maxBody:    maxBody,
	}
}

func newTunnelHTTPClient() *http.Client {
	return &http.Client{
		Timeout: sentryTunnelTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (h *sentryTunnel) configured() bool {
	return h != nil && h.host != "" && len(h.projects) > 0
}

func (h *sentryTunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != sentryEnvelopeMediaType {
		writeError(w, http.StatusBadRequest, "The envelope is not valid.")
		return
	}
	if r.ContentLength > h.maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "Request body is too large.")
		return
	}
	if retry, ok := h.limiter.allow(time.Now(), clientKey(r)); !ok {
		secs := int(retry.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", itoa(secs))
		writeError(w, http.StatusTooManyRequests, "Too many requests.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Request body is too large.")
			return
		}
		writeError(w, http.StatusBadRequest, "The envelope is not valid.")
		return
	}
	host, project, publicKey, err := envelopeTarget(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "The envelope is not valid.")
		return
	}
	if err := envelopeItems(body); err != nil {
		writeError(w, http.StatusBadRequest, "The envelope is not valid.")
		return
	}
	if host != h.host {
		writeError(w, http.StatusForbidden, "This envelope is not accepted.")
		return
	}
	if _, ok := h.projects[project]; !ok {
		writeError(w, http.StatusForbidden, "This envelope is not accepted.")
		return
	}
	if len(h.publicKeys) > 0 {
		if _, ok := h.publicKeys[publicKey]; !ok {
			writeError(w, http.StatusForbidden, "This envelope is not accepted.")
			return
		}
	}
	endpoint := "https://" + host + "/api/" + project + "/envelope/"
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, "The envelope is not valid.")
		return
	}
	upstream.Header.Set("Content-Type", sentryEnvelopeMediaType)
	resp, err := h.client.Do(upstream)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "The report could not be delivered."})
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
	if resp.StatusCode == http.StatusTooManyRequests {
		copyResponseHeader(w, resp, "Retry-After")
		copyResponseHeader(w, resp, "X-Sentry-Rate-Limits")
	}
	w.WriteHeader(resp.StatusCode)
}

func copyResponseHeader(w http.ResponseWriter, resp *http.Response, name string) {
	if v := resp.Header.Get(name); v != "" {
		w.Header().Set(name, v)
	}
}

func envelopeTarget(body []byte) (host, project, publicKey string, err error) {
	line, _, _ := bytes.Cut(body, []byte("\n"))
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", "", "", errInvalidEnvelope
	}
	var header struct {
		DSN string `json:"dsn"`
	}
	if err := json.Unmarshal(line, &header); err != nil || strings.TrimSpace(header.DSN) == "" {
		return "", "", "", errInvalidEnvelope
	}
	return dsnHostAndProject(header.DSN)
}

func dsnHostAndProject(raw string) (host, project, publicKey string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.Scheme != "https" {
		return "", "", "", errInvalidEnvelope
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	project = parts[len(parts)-1]
	if !projectIDPattern.MatchString(project) {
		return "", "", "", errInvalidEnvelope
	}
	if u.User != nil {
		publicKey = u.User.Username()
	}
	if publicKey == "" {
		return "", "", "", errInvalidEnvelope
	}
	return strings.ToLower(u.Hostname()), project, publicKey, nil
}

func envelopeItems(body []byte) error {
	_, rest, ok := bytes.Cut(body, []byte("\n"))
	if !ok {
		return errInvalidEnvelope
	}
	var sawItem bool
	for len(bytes.TrimSpace(rest)) > 0 {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			rest = after
			continue
		}
		var hdr struct {
			Type   string `json:"type"`
			Length *int   `json:"length"`
		}
		if err := json.Unmarshal(line, &hdr); err != nil || hdr.Type == "" || !allowedEnvelopeItem(hdr.Type) {
			return errInvalidEnvelope
		}
		sawItem = true
		if hdr.Length == nil {
			if !found {
				return errInvalidEnvelope
			}
			_, rest, _ = bytes.Cut(after, []byte("\n"))
			continue
		}
		if *hdr.Length < 0 || *hdr.Length > len(after) {
			return errInvalidEnvelope
		}
		rest = after[*hdr.Length:]
		if len(rest) > 0 {
			if rest[0] != '\n' {
				return errInvalidEnvelope
			}
			rest = rest[1:]
		}
	}
	if !sawItem {
		return errInvalidEnvelope
	}
	return nil
}

func allowedEnvelopeItem(typ string) bool {
	switch typ {
	case "event", "transaction", "session", "sessions", "client_report", "attachment":
		return true
	default:
		return false
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type tunnelLimiter struct {
	mu     sync.Mutex
	perIP  int
	global int
	window time.Duration
	hits   map[string][]time.Time
}

func newTunnelLimiter(perIP, global int, window time.Duration) *tunnelLimiter {
	return &tunnelLimiter{
		perIP:  perIP,
		global: global,
		window: window,
		hits:   map[string][]time.Time{},
	}
}

func (l *tunnelLimiter) allow(now time.Time, key string) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	if len(l.hits["global"]) >= l.global {
		return l.retry(now, "global"), false
	}
	if len(l.hits[key]) >= l.perIP {
		return l.retry(now, key), false
	}
	l.hits["global"] = append(l.hits["global"], now)
	l.hits[key] = append(l.hits[key], now)
	return 0, true
}

func (l *tunnelLimiter) sweep(now time.Time) {
	cut := now.Add(-l.window)
	for key, hits := range l.hits {
		i := 0
		for i < len(hits) && !hits[i].After(cut) {
			i++
		}
		if i == len(hits) {
			delete(l.hits, key)
			continue
		}
		if i > 0 {
			l.hits[key] = append([]time.Time(nil), hits[i:]...)
		}
	}
}

func (l *tunnelLimiter) retry(now time.Time, key string) time.Duration {
	hits := l.hits[key]
	if len(hits) == 0 {
		return time.Second
	}
	wait := hits[0].Add(l.window).Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}
