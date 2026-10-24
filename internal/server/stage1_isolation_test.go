package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Stage-1 routes are registered even when the listener chain is off, so the
// inventory can see them. Without that chain they answer not_found and must
// not echo another household.
func init() {
	routes := []struct {
		pattern string
		kind    isoKind
	}{
		{"GET /api/config", kindListLeak},
		{"GET /api/auth/session", kindListLeak},
		{"POST /api/auth/login", kindWriteScope},
		{"POST /api/auth/logout", kindWriteScope},
		{"POST /api/auth/reset", kindWriteScope},
		{"POST /api/auth/session/household", kindWriteScope},
		{"GET /api/auth/login", kindListLeak},
		{"GET /api/auth/callback", kindListLeak},
		{"GET /api/households", kindListLeak},
		{"POST /api/households", kindWriteScope},
		{"GET /api/households/{id}", kindForeignID},
		{"PATCH /api/households/{id}", kindForeignID},
		{"DELETE /api/households/{id}", kindForeignID},
		{"POST /api/households/{id}/restore", kindForeignID},
		{"GET /api/households/{id}/members", kindForeignID},
		{"PATCH /api/households/{id}/members/{userId}", kindForeignID},
		{"DELETE /api/households/{id}/members/{userId}", kindForeignID},
		{"POST /api/households/{id}/members/{userId}/reset-link", kindForeignID},
		{"GET /api/household/settings", kindListLeak},
		{"PATCH /api/household/settings", kindWriteScope},
		{"POST /api/household/household-password/retire", kindWriteScope},
		{"POST /api/household/household-password/restore", kindWriteScope},
		{"POST /api/invites", kindWriteScope},
		{"GET /api/invites", kindListLeak},
		{"POST /api/invites/{id}/revoke", kindForeignID},
		{"POST /api/invites/preview", kindWriteScope},
		{"POST /api/invites/accept", kindWriteScope},
		{"POST /api/operator/household-invites", kindWriteScope},
		{"GET /api/operator/alerts", kindListLeak},
		{"POST /api/devices/pair", kindWriteScope},
		{"GET /api/devices/pair/{id}", kindForeignID},
		{"POST /api/devices/pair/complete", kindWriteScope},
		{"GET /api/devices", kindListLeak},
		{"PATCH /api/devices/{id}", kindForeignID},
		{"DELETE /api/devices/{id}", kindForeignID},
		{"POST /api/device/scans", kindWriteScope},
		{"POST /api/device/heartbeat", kindWriteScope},
		{"POST /api/device/token/rotate", kindWriteScope},
		{"DELETE /api/me", kindWriteScope},
	}
	for _, route := range routes {
		isolationCases[route.pattern] = isolationCase{
			kind:   route.kind,
			reason: "stage-1 membership is checked before any household data is returned",
			check:  stage1HidesHouseholds(route.pattern),
		}
	}
}

func stage1HidesHouseholds(pattern string) func(*testing.T, *twoHouseholds) {
	return func(t *testing.T, fx *twoHouseholds) {
		t.Helper()
		req, err := requestForPattern(pattern)
		if err != nil {
			t.Fatalf("request %s: %v", pattern, err)
		}
		fx.principalFor(fx.Alder).Apply(req)
		rec := httptest.NewRecorder()
		fx.Handler.ServeHTTP(rec, req)
		body, err := io.ReadAll(rec.Result().Body)
		if err != nil {
			t.Fatalf("read %s: %v", pattern, err)
		}
		if rec.Code == http.StatusOK {
			t.Fatalf("%s returned 200 without a stage-1 session: %s", pattern, body)
		}
		assertNoLeak(t, body, fx.Birch)
		assertNoLeak(t, body, fx.Alder)
	}
}
