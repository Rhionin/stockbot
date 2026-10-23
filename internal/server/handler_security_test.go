package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersOnHealth(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "health responses name the browser policy and are not cached",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/health",
				expectedStatus: http.StatusOK,
				expectedHeaders: map[string]string{
					"X-Content-Type-Options":  "nosniff",
					"X-Frame-Options":         "DENY",
					"Referrer-Policy":         "no-referrer",
					"Content-Security-Policy": browserCSP,
					"Permissions-Policy":      "camera=(self), microphone=(), geolocation=()",
					"Cache-Control":           "no-store",
				},
			},
		},
	})
}

func TestRejectsOversizedJSONBody(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "a body over one mebibyte is rejected",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"request body is too large"},
			},
		},
	})
}

func TestWriteErrorHidesServerDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusInternalServerError, codeInternal, `sql: no such table: products`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"error":"Something went wrong. Try again.","code":"internal"}` {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestWriteJSONDropsPrivateImageURL(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, struct {
		ImageURL string `json:"imageUrl,omitempty"`
		Name     string `json:"name"`
	}{ImageURL: "http://127.0.0.1/secret.jpg", Name: "Milk"})
	if strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Fatalf("body kept private image URL: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Milk") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
