package kroger

import (
	"context"
	"net/http"
	"testing"
)

func TestTestAppKeysUnexpected4xxAreWrongKeys(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"429", http.StatusTooManyRequests, `{"error":"slow_down"}`},
		{"400", http.StatusBadRequest, `{"error":"invalid_request"}`},
		{"404", http.StatusNotFound, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := stubTransport(func(*http.Request) (*http.Response, error) {
				return jsonResp(tc.status, tc.body), nil
			})
			ok, code, pa := TestAppKeys(context.Background(), BaseURL, tr, "id", "secret", "https://cb.example")
			if ok || code != "wrong_keys" || pa != nil {
				t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
			}
		})
	}
}
