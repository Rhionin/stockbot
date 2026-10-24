package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncSnapshotRefusesAMissingTokenAndAFailedRead(t *testing.T) {
	_, env := setupTestWithDB(t)
	handler := &syncSnapshotHandler{DB: env.DB}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("empty token status = %d %s", rec.Code, rec.Body.String())
	}

	handler.Token = "snapshot-token"
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil)
	req.Header.Set("Authorization", "Bearer other-token")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil)
	req.Header.Set("Authorization", "Bearer snapshot-token")
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("missing household was served: %s", rec.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req = withHouseholdScope(t, httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil).WithContext(ctx))
	req.Header.Set("Authorization", "Bearer snapshot-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelled read wrote %s", rec.Body.String())
	}
}
