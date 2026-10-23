package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/go-json-experiment/json"
)

// maxJSONBodyBytes caps a JSON request. Inventory and scan payloads are small;
// a larger body is rejected instead of being buffered.
const maxJSONBodyBytes = 1 << 20

var pathParamRegExp = regexp.MustCompile(`\/{(.*?)}`)

// Request wraps handler input with context, body, and path parameters.
type Request[TBody, TPathParams any] struct {
	Context    context.Context
	Body       TBody
	PathParams TPathParams
	RawRequest *http.Request
}

// Created wraps a response value to signal a 201 Created status.
type Created struct {
	Value any
}

// SeeOther redirects the browser. Location must not carry a credential.
type SeeOther struct {
	Location string
}

// NoContent is a 204 response with an empty body.
type NoContent struct{}

type errorPayload struct {
	Error       string `json:"error"`
	Code        string `json:"code,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Members     any    `json:"members,omitempty"`
	Suggestions any    `json:"suggestions,omitempty"`
}

// handlerFunc is a handler function that takes a parsed request and returns a response.
type handlerFunc[TBody, TPathParams, TResp any] func(req Request[TBody, TPathParams]) (TResp, error)

// HandleJSON wraps a handler function with JSON parsing and response writing.
// Success defaults to 200 OK, or 201 Created if the response type is Created.
// Errors are converted to HTTP status codes via httpStatusFromError.
// The pattern is extracted from r.Pattern at runtime (Go 1.22+).
func HandleJSON[TBody, TPathParams, TResp any](fn handlerFunc[TBody, TPathParams, TResp]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body TBody

		if r.ContentLength > maxJSONBodyBytes {
			writeError(w, http.StatusBadRequest, codePayloadTooLarge, "request body is too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusBadRequest, codePayloadTooLarge, "request body is too large")
				return
			}
			writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("invalid request body: %v", err))
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("invalid request body: %v", err))
				return
			}
		}

		pattern := r.Pattern
		pathParams, err := parsePathParams[TPathParams](r, pattern)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("invalid path parameters: %v", err))
			return
		}

		req := Request[TBody, TPathParams]{
			Context:    r.Context(),
			Body:       body,
			PathParams: pathParams,
			RawRequest: r,
		}

		resp, err := fn(req)
		if err != nil {
			writeStatusError(r, w, httpStatusFromError(err), err)
			return
		}

		if redir, ok := any(resp).(SeeOther); ok {
			http.Redirect(w, r, redir.Location, http.StatusSeeOther)
			return
		}
		if _, ok := any(resp).(NoContent); ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		status := http.StatusOK
		var respData any = resp

		if created, ok := any(resp).(Created); ok {
			status = http.StatusCreated
			respData = created.Value
		}

		writeJSON(w, status, respData)
	}
}

// parsePathParams extracts path parameters from the request using the pattern.
func parsePathParams[Pp any](r *http.Request, pattern string) (Pp, error) {
	matches := pathParamRegExp.FindAllStringSubmatch(pattern, -1)

	rawMap := map[string]string{}
	for _, match := range matches {
		rawMap[match[1]] = r.PathValue(match[1])
	}
	var pp Pp

	d, err := json.Marshal(rawMap)
	if err != nil {
		return pp, err
	}

	if err := json.Unmarshal(d, &pp); err != nil {
		return pp, err
	}
	return pp, nil
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, scrubImageURLs(data)); err != nil {
		fmt.Printf("error encoding JSON response: %v\n", err)
	}
}

// writeError writes a JSON error response. A 5xx status keeps the underlying
// detail in the server log and returns a fixed sentence, so a database or
// upstream message is not part of the HTTP body. code is the app-facing code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeStatusError(nil, w, status, &HTTPError{Code: status, Message: message, ErrorCode: code})
}

// writeExemptError writes a JSON error with no code. The browser tunnel and
// the two unconfigured sign-in responses are the only callers. A code on
// those bodies is a contract change.
func writeExemptError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorPayload{Error: message})
}

// writeHTTPError writes a JSON error. A 409 can include a code, a member list, and suggestions.
func writeHTTPError(w http.ResponseWriter, status int, err error) {
	writeStatusError(nil, w, status, err)
}

// writeStatusError writes a JSON error. A 5xx is also reported, with the
// request when one is available. A classified busy or no-space failure keeps
// its own sentence. provider_unavailable keeps its sentence and provider.
// Every other 5xx uses the generic sentence and code internal. Driver text
// stays in the server log. A 4xx that did not name a code gets the default
// for its status. An explicit code, including a commit code, is kept.
func writeStatusError(r *http.Request, w http.ResponseWriter, status int, err error) {
	if status >= 500 {
		reportServerError(r, err)
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	var httpErr *HTTPError
	var code string
	var provider string
	var members any
	var suggestions any
	if errors.As(err, &httpErr) {
		if httpErr.Message != "" {
			message = httpErr.Message
		}
		code = httpErr.ErrorCode
		provider = httpErr.Provider
		members = httpErr.Members
		suggestions = httpErr.Suggestions
	}
	if status >= 500 {
		// A full disk has its own warning line. The driver text is not
		// copied into the request-failed log.
		if sqliteNoSpace(err) {
			logNoSpace(r, err)
		} else {
			logged := message
			if httpErr != nil && httpErr.Err != nil {
				logged = httpErr.Err.Error()
			}
			if errors.Is(err, context.Canceled) {
				slog.Info("request canceled")
			} else if !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("request failed: %s", logged)
			}
		}
		switch {
		case sqliteBusy(err) || sqliteNoSpace(err):
			message, code, _ = serverClientPayload(err, code)
			provider = ""
			members = nil
			suggestions = nil
		case code == codeProviderUnavailable:
			members = nil
			suggestions = nil
		default:
			message = clientmsg.Generic
			code = codeInternal
			provider = ""
			members = nil
			suggestions = nil
		}
	} else if code == "" {
		code = defaultClientCode(status)
	}
	writeJSON(w, status, errorPayload{
		Error:       message,
		Code:        code,
		Provider:    provider,
		Members:     members,
		Suggestions: suggestions,
	})
}

// apiErrors turns a router miss on /api into JSON. A matched route, including
// a trailing-slash redirect, is passed through. /health is not wrapped, so a
// wrong method there stays the router's plain text.
func apiErrors(next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := next.Handler(r); pattern != "" {
			next.ServeHTTP(w, r)
			return
		}
		probe := &captureResponse{header: make(http.Header)}
		next.ServeHTTP(probe, r)
		if probe.code == http.StatusMethodNotAllowed {
			if allow := probe.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, "Method not allowed.")
			return
		}
		writeError(w, http.StatusNotFound, codeNotFound, "Not found.")
	})
}

// captureResponse records a probe of the router's own 404 or 405.
type captureResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *captureResponse) Header() http.Header { return c.header }

func (c *captureResponse) WriteHeader(status int) {
	if c.code == 0 {
		c.code = status
	}
}

func (c *captureResponse) Write(p []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(p)
}
