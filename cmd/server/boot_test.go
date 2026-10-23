package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootGateServesMigratingThenReady(t *testing.T) {
	gate := newBootGate()
	srv := httptest.NewServer(gate)
	t.Cleanup(srv.Close)

	health := getBody(t, srv.URL+"/health")
	if health.code != http.StatusServiceUnavailable {
		t.Fatalf("GET /health status = %d, want 503; body = %q", health.code, health.body)
	}
	if health.body != migratingBody {
		t.Fatalf("GET /health body = %q, want updating", health.body)
	}

	build := getBody(t, srv.URL+"/api/build")
	if build.code != http.StatusOK {
		t.Fatalf("GET /api/build status = %d, want 200; body = %q", build.code, build.body)
	}
	if !strings.Contains(build.body, `"commit"`) {
		t.Fatalf("GET /api/build body = %q, want a commit", build.body)
	}

	other := getBody(t, srv.URL+"/api/inventory")
	if other.code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/inventory status = %d, want 503 while migrating", other.code)
	}
	if other.body != migratingBody {
		t.Fatalf("GET /api/inventory body = %q, want updating", other.body)
	}
	var buildJSON map[string]any
	if err := json.Unmarshal([]byte(build.body), &buildJSON); err != nil {
		t.Fatalf("GET /api/build body = %q: %v", build.body, err)
	}
	if _, ok := buildJSON["code"]; ok {
		t.Fatalf("GET /api/build included an error code: %s", build.body)
	}

	gate.use(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmtOK(w)
	}))

	ready := getBody(t, srv.URL+"/health")
	if ready.code != http.StatusOK {
		t.Fatalf("GET /health after ready status = %d, want 200; body = %q", ready.code, ready.body)
	}
	if !strings.Contains(ready.body, `"status":"ok"`) {
		t.Fatalf("GET /health after ready body = %q, want ok", ready.body)
	}
}

const migratingBody = `{"status":"migrating","error":"Updating. Try again in a minute.","code":"updating"}` + "\n"

func fmtOK(w http.ResponseWriter) (int, error) {
	return io.WriteString(w, `{"status":"ok"}`+"\n")
}

type bodyResult struct {
	code int
	body string
}

func getBody(t *testing.T, url string) bodyResult {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return bodyResult{code: res.StatusCode, body: string(body)}
}
