package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestSyncSnapshotReadsWhileTheWriterIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	writer, reader, err := app.OpenPantry(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reader.Close()
		writer.Close()
	})
	if err := app.RunMigrations(writer); err != nil {
		t.Fatal(err)
	}
	handler, _ := NewHandler(nil, nil, nil, writer, WithSyncToken("sync-token-value-ok"), WithReadDB(reader))

	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`INSERT INTO app_settings (key, value) VALUES ('lock-probe', '1')`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer sync-token-value-ok")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"products":[]`) {
		t.Fatalf("body %s", rec.Body.String())
	}
}
