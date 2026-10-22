package server

import (
	"net/url"
	"strings"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/getsentry/sentry-go"
)

// scrubSentryEvent removes request bodies, cookies, credentials, query
// strings, and free-text that can carry a household name, note, or email.
// The request keeps its path, including ids, and only the Content-Type and
// User-Agent headers. The same function runs for errors and transactions.
func scrubSentryEvent(event *sentry.Event) *sentry.Event {
	if event == nil {
		return nil
	}
	event.User = sentry.User{}
	event.Extra = nil
	event.Request = scrubSentryRequest(event.Request)
	event.Contexts = scrubContexts(event.Contexts)
	event.Tags = scrubTags(event.Tags)
	for i := range event.Breadcrumbs {
		event.Breadcrumbs[i] = scrubSentryBreadcrumb(event.Breadcrumbs[i])
	}
	event.Breadcrumbs = compactBreadcrumbs(event.Breadcrumbs)
	typ := ""
	for i := range event.Exception {
		ex := &event.Exception[i]
		if typ == "" {
			typ = ex.Type
		}
		ex.Value = genericServerMessage(ex.Type)
		if ex.Mechanism != nil {
			ex.Mechanism.Description = ""
			ex.Mechanism.Data = nil
		}
	}
	// The no-space warning is a fixed sentence with no household data.
	// Replacing it would hide the report CaptureMessage sent.
	if event.Message != "" && event.Message != clientmsg.NoSpace {
		event.Message = genericServerMessage(typ)
	}
	if typ != "" {
		event.Fingerprint = []string{"{{ default }}", typ}
	}
	for _, span := range event.Spans {
		scrubSpan(span)
	}
	event.Transaction = stripQuery(event.Transaction)
	return event
}

func scrubSentryBreadcrumb(crumb *sentry.Breadcrumb) *sentry.Breadcrumb {
	if crumb == nil {
		return nil
	}
	crumb.Message = ""
	next := map[string]interface{}{}
	if crumb.Data != nil {
		if method, ok := crumb.Data["method"].(string); ok && method != "" {
			next["method"] = method
		}
		if status, ok := crumb.Data["status_code"]; ok {
			next["status_code"] = status
		}
		if raw, ok := crumb.Data["url"].(string); ok && raw != "" {
			next["url"] = requestPath(raw)
		}
	}
	if len(next) == 0 {
		crumb.Data = nil
	} else {
		crumb.Data = next
	}
	return crumb
}

func scrubSentryRequest(req *sentry.Request) *sentry.Request {
	if req == nil {
		return nil
	}
	req.URL = requestPath(req.URL)
	req.QueryString = ""
	req.Data = ""
	req.Cookies = ""
	req.Env = nil
	req.Headers = scrubHeaders(req.Headers)
	return req
}

func scrubHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, 2)
	for name, value := range headers {
		if !allowedRequestHeader(name) {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func allowedRequestHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "content-type", "user-agent":
		return true
	default:
		return false
	}
}

func scrubTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		if !keepTag(key, value) {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func keepTag(key, value string) bool {
	switch strings.ToLower(key) {
	case "route", "transaction", "status", "method", "runtime", "runtime.name",
		"os", "os.name", "server_name", "release", "environment", "device", "device.family",
		"browser", "browser.name", "level", "mechanism", "handled", "http.status_code",
		"db_busy", "db_no_space":
	default:
		return false
	}
	if strings.Contains(value, "@") || strings.ContainsAny(value, "?&=") {
		return false
	}
	return true
}

func scrubContexts(contexts map[string]sentry.Context) map[string]sentry.Context {
	if len(contexts) == 0 {
		return nil
	}
	out := make(map[string]sentry.Context, len(contexts))
	for _, key := range []string{"trace", "os", "device", "runtime", "browser", "app"} {
		if ctx, ok := contexts[key]; ok {
			out[key] = ctx
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scrubSpan(span *sentry.Span) {
	if span == nil {
		return
	}
	span.Name = stripQuery(span.Name)
	span.Description = stripQuery(span.Description)
	if span.Data == nil {
		return
	}
	next := make(map[string]interface{}, len(span.Data))
	for key, value := range span.Data {
		if sensitiveDataKey(key) {
			continue
		}
		if raw, ok := value.(string); ok {
			value = stripQuery(raw)
		}
		next[key] = value
	}
	span.Data = next
}

func sensitiveDataKey(key string) bool {
	switch strings.ToLower(key) {
	case "body", "data", "cookies", "cookie", "query", "query_string", "authorization",
		"http.request.body", "note", "name", "email", "item":
		return true
	default:
		return false
	}
}

func compactBreadcrumbs(in []*sentry.Breadcrumb) []*sentry.Breadcrumb {
	if len(in) == 0 {
		return nil
	}
	out := in[:0]
	for _, crumb := range in {
		if crumb != nil {
			out = append(out, crumb)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func genericServerMessage(typ string) string {
	if typ == "" {
		typ = "error"
	}
	return "server error (" + typ + ")"
}

func requestPath(raw string) string {
	raw = stripQuery(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	return u.Path
}

func stripQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}
