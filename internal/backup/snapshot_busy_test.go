package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func intPtr(n int) *int { return &n }

// deleteJournalReservedWriter opens a delete-journal DB with a writer holding RESERVED.
func deleteJournalReservedWriter(t *testing.T) (path string, release func()) {
	t.Helper()
	path = fixtureDB(t)
	h, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	h.SetMaxOpenConns(1)
	var mode string
	if err := h.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal %q %v", mode, err)
	}
	if _, err := h.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`INSERT INTO products (id, barcode, name) VALUES ('p2', 'x', 'Invented Fixture')`); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		_, _ = h.Exec(`ROLLBACK`)
		h.Close()
	}
}

// TestSnapshotSourceTxlockDeferred pins deferred txlock on the snapshot source connection.
func TestSnapshotSourceTxlockDeferred(t *testing.T) {
	path, release := deleteJournalReservedWriter(t)
	defer release()
	db, err := openSource(path, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("source Begin blocked by a pending writer (txlock not deferred): %v", err)
	}
	_ = tx.Rollback()
}

func TestSnapshotUnderReservedWriter(t *testing.T) {
	path, release := deleteJournalReservedWriter(t)
	defer release()
	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := Snapshot(path, dest, SnapshotOptions{
		BusyTimeout: 200 * time.Millisecond,
		Retries:     0,
		RetryWait:   time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsNegativeRetries(t *testing.T) {
	path := fixtureDB(t)
	dest := filepath.Join(t.TempDir(), "snap.db")
	err := Snapshot(path, dest, SnapshotOptions{BusyTimeout: time.Second, Retries: -1})
	if err == nil {
		t.Fatal("expected an error for negative retries")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("negative retries must not create a snapshot file")
	}
}

// TestRunnerRunRetriesBusyByDefault pins the unset Runner busy-retry default (3).
func TestRunnerRunRetriesBusyByDefault(t *testing.T) {
	path := fixtureDB(t)
	h, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetMaxOpenConns(1)
	if _, err := h.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	const retryWait = 7 * time.Millisecond
	busySleeps := 0
	released := false
	recips, _ := testRecipient(t)
	r := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: recips, Store: newMemStore(time.Now), BusyTimeout: 20 * time.Millisecond,
		BusyRetryWait: retryWait,
		Sleep: func(d time.Duration) {
			if d != retryWait {
				return
			}
			busySleeps++
			if busySleeps == 3 && !released {
				_, _ = h.Exec(`COMMIT`)
				released = true
			}
		},
	}
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with default BusyRetries failed after %d busy sleeps: %v", busySleeps, err)
	}
	if busySleeps != 3 {
		t.Fatalf("busy sleeps = %d, want 3 (default 3 retries)", busySleeps)
	}
}

func TestRunnerExplicitZeroBusyRetries(t *testing.T) {
	path := fixtureDB(t)
	h, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetMaxOpenConns(1)
	if _, err := h.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	busySleeps := 0
	recips, _ := testRecipient(t)
	zero := 0
	r := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: recips, Store: newMemStore(time.Now), BusyTimeout: 20 * time.Millisecond,
		BusyRetries: intPtr(zero), BusyRetryWait: time.Millisecond,
		Sleep: func(time.Duration) { busySleeps++ },
	}
	_, err = r.Run(context.Background())
	if err == nil {
		t.Fatal("expected snapshot to fail with no busy retries")
	}
	if busySleeps != 0 {
		t.Fatalf("busy sleeps = %d, want 0 when BusyRetries is explicitly 0", busySleeps)
	}
}

func TestRunnerUnsetBusyRetriesDefaultsToThree(t *testing.T) {
	if got := (&Runner{}).busyRetries(); got != 3 {
		t.Fatalf("busyRetries = %d, want 3 when unset", got)
	}
}
