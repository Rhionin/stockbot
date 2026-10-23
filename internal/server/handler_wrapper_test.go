package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/inventory"
)

func TestWrappedHandlerGenericServerError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{
			name:   "plain error",
			err:    errors.New("sql: database is locked"),
			status: http.StatusServiceUnavailable,
			body:   `{"error":"Stockbot is busy right now. Try again in a moment.","code":"busy"}`,
		},
		{
			name:   "bad gateway",
			err:    BadGateway("could not refresh product from Open Food Facts"),
			status: http.StatusBadGateway,
			body:   `{"error":"Something went wrong. Try again."}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
				return struct{}{}, tc.err
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(""))
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != tc.body {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestWrappedHandlerInternalCodeUsesSharedSentence(t *testing.T) {
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, &HTTPError{
			Code:      http.StatusInternalServerError,
			Message:   "sql: database is locked",
			ErrorCode: inventory.CodeInternal,
			Err:       errors.New("sql: database is locked"),
		}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(""))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"error":"Something went wrong. Try again.","code":"internal"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
