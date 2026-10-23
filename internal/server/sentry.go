package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

const sentryFlushTimeout = 2 * time.Second

// sentryTracesSampleRate is the fraction of ordinary requests traced.
// The Pi is a small host, and /health and the event stream are not traced.
// Tests set this to 1 and restore it.
var sentryTracesSampleRate = 0.1

// sentryEnabled is set only after InitSentry accepts a DSN. An empty DSN
// leaves the SDK uninitialized so the process makes no Sentry calls.
var sentryEnabled atomic.Bool

// sentryTestTransport replaces the network transport in tests.
var sentryTestTransport sentry.Transport

// SentrySettings is the process-wide error reporting configuration.
// An empty DSN leaves both the SDK and the browser tunnel off.
type SentrySettings struct {
	DSN         string
	Environment string
	IngestHost  string
	ProjectIDs  map[string]struct{}
	PublicKeys  map[string]struct{}
	// Rejected is true when SENTRY_DSN was set but could not be parsed.
	// The value itself is never kept.
	Rejected bool
}

// LoadSentrySettings reads Sentry configuration from the environment.
// A missing or unusable DSN returns a zero setting so callers stay quiet
// and make no network calls.
func LoadSentrySettings(getenv func(string) string) SentrySettings {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	rawDSN := strings.TrimSpace(getenv("SENTRY_DSN"))
	settings := SentrySettings{
		Environment: strings.TrimSpace(getenv("SENTRY_ENVIRONMENT")),
		ProjectIDs:  parseProjectIDs(getenv("SENTRY_TUNNEL_PROJECT_IDS")),
		PublicKeys:  parsePublicKeys(getenv("SENTRY_TUNNEL_PUBLIC_KEYS")),
	}
	if rawDSN == "" {
		return settings
	}
	host, err := ingestHost(rawDSN)
	if err != nil {
		settings.Rejected = true
		return settings
	}
	settings.DSN = rawDSN
	settings.IngestHost = host
	if settings.Environment == "" {
		settings.Environment = "production"
	}
	return settings
}

// TunnelConfig is the browser tunnel derived from the same DSN host.
func (s SentrySettings) TunnelConfig() SentryTunnelConfig {
	return SentryTunnelConfig{
		IngestHost: s.IngestHost,
		ProjectIDs: s.ProjectIDs,
		PublicKeys: s.PublicKeys,
	}
}

// InitSentry configures the SDK from settings. An empty DSN does not call
// the SDK. Release is the commit GET /api/build reports.
func InitSentry(settings SentrySettings) error {
	if settings.DSN == "" {
		sentryEnabled.Store(false)
		return nil
	}
	options := sentry.ClientOptions{
		Dsn:              settings.DSN,
		Environment:      settings.Environment,
		Release:          buildinfo.Commit,
		SendDefaultPII:   false,
		EnableTracing:    true,
		TracesSampleRate: sentryTracesSampleRate,
		TracesSampler:    sentry.TracesSampler(sentryTracesSampler),
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubSentryEvent(event)
		},
		BeforeSendTransaction: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubSentryEvent(event)
		},
		BeforeBreadcrumb: func(crumb *sentry.Breadcrumb, _ *sentry.BreadcrumbHint) *sentry.Breadcrumb {
			return scrubSentryBreadcrumb(crumb)
		},
		Transport: sentryTestTransport,
	}
	err := sentry.Init(options)
	if err != nil {
		sentryEnabled.Store(false)
		return err
	}
	sentryEnabled.Store(true)
	return nil
}

// FlushSentry waits up to two seconds for queued events. It does nothing
// when reporting is off.
func FlushSentry() {
	if !sentryEnabled.Load() {
		return
	}
	sentry.Flush(sentryFlushTimeout)
}

func withSentry(next http.Handler) http.Handler {
	if next == nil || !sentryEnabled.Load() {
		return next
	}
	// The mux sets r.Pattern while next runs. Rename before sentryhttp
	// finishes the transaction, so the name is the route and not the raw path.
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer nameSentryTransaction(r)
		next.ServeHTTP(w, r)
	})
	return sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(wrapped)
}

func nameSentryTransaction(r *http.Request) {
	if r == nil {
		return
	}
	span := sentry.SpanFromContext(r.Context())
	if span == nil {
		return
	}
	if r.Pattern == "" {
		span.Name = r.Method + " <unmatched>"
		span.Source = sentry.SourceCustom
		return
	}
	name := r.Pattern
	if !strings.Contains(name, " ") {
		name = r.Method + " " + name
	}
	span.Name = name
	span.Source = sentry.SourceRoute
}

func sentryTracesSampler(ctx sentry.SamplingContext) float64 {
	if ctx.Span == nil || traceExcluded(ctx.Span.Name) {
		return 0
	}
	return sentryTracesSampleRate
}

func traceExcluded(name string) bool {
	path := strings.TrimSpace(name)
	for {
		method, rest, ok := strings.Cut(path, " ")
		if !ok || !isHTTPMethod(method) {
			break
		}
		path = strings.TrimSpace(rest)
	}
	path = strings.TrimSuffix(path, "/")
	return path == "/health" || path == "/api/events"
}

func isHTTPMethod(s string) bool {
	switch s {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func reportServerError(r *http.Request, err error) {
	if err == nil || errors.Is(err, context.Canceled) || !sentryEnabled.Load() {
		return
	}
	hub := sentry.CurrentHub()
	if r != nil {
		if scoped := sentry.GetHubFromContext(r.Context()); scoped != nil {
			hub = scoped
		}
	}
	if sqliteBusy(err) {
		hub.WithScope(func(scope *sentry.Scope) {
			scope.SetLevel(sentry.LevelWarning)
			scope.SetTag("db_busy", "true")
			hub.CaptureMessage(clientmsg.Busy)
		})
		return
	}
	if sqliteNoSpace(err) {
		hub.WithScope(func(scope *sentry.Scope) {
			scope.SetLevel(sentry.LevelWarning)
			scope.SetTag("db_no_space", "true")
			hub.CaptureMessage(clientmsg.NoSpace)
		})
		return
	}
	hub.CaptureException(err)
}

func ingestHost(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User == nil {
		return "", errInvalidEnvelope
	}
	return strings.ToLower(u.Hostname()), nil
}

func parseProjectIDs(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if projectIDPattern.MatchString(part) {
			out[part] = struct{}{}
		}
	}
	return out
}

func parsePublicKeys(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = struct{}{}
		}
	}
	return out
}
