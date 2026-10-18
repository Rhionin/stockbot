package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"

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
			writeError(w, http.StatusBadRequest, "request body is too large")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusBadRequest, "request body is too large")
				return
			}
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
				return
			}
		}

		pattern := r.Pattern
		pathParams, err := parsePathParams[TPathParams](r, pattern)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid path parameters: %v", err))
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
			writeHTTPError(w, httpStatusFromError(err), err)
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
// upstream message is not part of the HTTP body.
func writeError(w http.ResponseWriter, status int, message string) {
	writeHTTPError(w, status, errors.New(message))
}

// writeHTTPError writes a JSON error. A 409 can include a code, a member list, and suggestions.
func writeHTTPError(w http.ResponseWriter, status int, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	var httpErr *HTTPError
	var code string
	var members any
	var suggestions any
	extra := false
	if errors.As(err, &httpErr) {
		if httpErr.Message != "" {
			message = httpErr.Message
		}
		if httpErr.ErrorCode != "" || httpErr.Members != nil || httpErr.Suggestions != nil {
			extra = true
			code = httpErr.ErrorCode
			members = httpErr.Members
			suggestions = httpErr.Suggestions
		}
	}
	if status >= 500 {
		logged := message
		if httpErr != nil && httpErr.Err != nil {
			logged = httpErr.Err.Error()
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("request failed: %s", logged)
		}
		// code internal is a deliberate client contract: the sentence is safe
		// and the underlying error was logged above. Other 500s stay generic
		// and omit the code.
		if httpErr != nil && httpErr.ErrorCode == "internal" && httpErr.Message != "" {
			message = httpErr.Message
			code = httpErr.ErrorCode
			members = nil
			suggestions = nil
			extra = true
		} else {
			message = "Something went wrong. Please try again."
			extra = false
		}
	}
	if extra {
		writeJSON(w, status, errorPayload{Error: message, Code: code, Members: members, Suggestions: suggestions})
		return
	}
	writeJSON(w, status, map[string]string{"error": message})
}
