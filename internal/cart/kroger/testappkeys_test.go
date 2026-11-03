package kroger

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stubTransport func(*http.Request) (*http.Response, error)

func (f stubTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestTestAppKeysOK(t *testing.T) {
	tr := stubTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || !strings.Contains(req.URL.Path, "/oauth2/token") {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		}
		return jsonResp(http.StatusOK, `{}`), nil
	})
	ok, code, pa := TestAppKeys(context.Background(), "https://api.kroger.com", tr, "id", "secret", "https://cb.example")
	if !ok || code != "" || pa == nil || *pa != 1 {
		t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
	}
}

func TestTestAppKeysWrongKeys(t *testing.T) {
	tr := stubTransport(func(*http.Request) (*http.Response, error) {
		return jsonResp(http.StatusUnauthorized, `{"error":"invalid_client"}`), nil
	})
	ok, code, pa := TestAppKeys(context.Background(), "", tr, "id", "bad", "")
	if ok || code != "wrong_keys" || pa != nil {
		t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
	}
}

func TestTestAppKeysNoProductAccess(t *testing.T) {
	tr := stubTransport(func(*http.Request) (*http.Response, error) {
		return jsonResp(http.StatusForbidden, `{"error":"invalid_scope"}`), nil
	})
	ok, code, pa := TestAppKeys(context.Background(), BaseURL, tr, "id", "secret", "")
	if !ok || code != "no_product_access" || pa == nil || *pa != 0 {
		t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
	}
}

func TestTestAppKeysUnreachable(t *testing.T) {
	tr := stubTransport(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})
	ok, code, pa := TestAppKeys(context.Background(), BaseURL, tr, "id", "secret", "")
	if ok || code != "kroger_unreachable" || pa != nil {
		t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
	}
}

func TestTestAppKeysServerError(t *testing.T) {
	tr := stubTransport(func(*http.Request) (*http.Response, error) {
		return jsonResp(http.StatusBadGateway, ``), nil
	})
	ok, code, pa := TestAppKeys(context.Background(), BaseURL, tr, "id", "secret", "")
	if ok || code != "kroger_unreachable" || pa != nil {
		t.Fatalf("ok=%v code=%q pa=%v", ok, code, pa)
	}
}
