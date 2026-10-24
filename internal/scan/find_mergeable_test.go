package scan

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// newTestQueueInternal mirrors the newTestQueue helper in scan_test.go
// (package scan_test), but lives in package scan so tests here have
// white-box access to findMergeableScanEntry, an unexported method on Queue.
func newTestQueueInternal(t *testing.T) (*Queue, *sql.DB, tenant.Scope) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	// One connection keeps the schema. A second :memory: connection is an
	// empty database, and a canceled query can check that one out of the pool.
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatalf("PrimaryScope: %v", err)
	}
	return NewQueue(conn), conn, scope
}

// insertRawScanEntry inserts a scan_entries row directly via SQL, bypassing
// CreateScanEntry's own merge-or-create check. This lets tests construct
// scenarios - like more than one open mergeable entry for the same key -
// that CreateScanEntry alone never produces, so findMergeableScanEntry's
// scanned_at tie-breaker can be exercised directly.
func insertRawScanEntry(t *testing.T, db *sql.DB, e ScanEntry) {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatalf("PrimaryScope: %v", err)
	}
	var directionStr *string
	if e.Direction != nil {
		directionStr = ptr(string(*e.Direction))
	}
	userID := e.UserID
	if userID == "" {
		userID = scope.HouseholdID()
	}
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO scan_entries (id, user_id, household_id, barcode, scanned_at, direction, unit_count, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, userID, scope.HouseholdID(), e.Barcode, e.ScannedAt, nullableString(directionStr), e.UnitCount, string(e.Status),
	)
	if err != nil {
		t.Fatalf("insertRawScanEntry: %v", err)
	}
}

// TestFindMergeableScanEntry is a direct unit test for the unexported
// findMergeableScanEntry query method, isolating cases that
// TestCreateScanEntry_* in scan_test.go don't exercise directly: exact
// matches, the IS-based null-safe direction comparison, exclusion of closed
// (committed/cancelled) entries, and the scanned_at tie-breaker when more
// than one mergeable entry exists.
func TestFindMergeableScanEntry(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		setup     func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope)
		userID    string
		barcode   string
		direction *ScanDirection
		wantID    string // "" means want nil
	}{
		{
			name: "exact match on user/barcode/direction/status returns the entry",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "111111111111", ScannedAt: now,
					Direction: ptr(StockIn), UnitCount: 1, Status: Pending,
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "111111111111", direction: ptr(StockIn),
			wantID: "scan-1",
		},
		{
			name:      "no entries at all returns nil",
			setup:     func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {},
			userID:    "actor",
			barcode:   "222222222222",
			direction: ptr(StockIn),
			wantID:    "",
		},
		{
			name: "nil query direction does not match a candidate with a non-nil direction",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "333333333333", ScannedAt: now,
					Direction: ptr(StockIn), UnitCount: 1, Status: Pending,
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "333333333333", direction: nil,
			wantID: "",
		},
		{
			name: "non-nil query direction does not match a candidate with a nil direction",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "444444444444", ScannedAt: now,
					UnitCount: 1, Status: Pending, // Direction left nil
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "444444444444", direction: ptr(StockIn),
			wantID: "",
		},
		{
			name: "nil query direction matches a candidate whose direction is also nil (IS null-safe equality)",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "555555555555", ScannedAt: now,
					UnitCount: 1, Status: Pending, // Direction left nil
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "555555555555", direction: nil,
			wantID: "scan-1",
		},
		{
			name: "only a committed candidate exists: returns nil",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "666666666666", ScannedAt: now,
					Direction: ptr(StockIn), UnitCount: 1, Status: Committed,
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "666666666666", direction: ptr(StockIn),
			wantID: "",
		},
		{
			name: "only a cancelled candidate exists: returns nil",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				if _, err := q.CreateScanEntry(ctx, scope, ScanEntry{
					ID: "scan-1", UserID: "actor", Barcode: "777777777777", ScannedAt: now,
					Direction: ptr(StockIn), UnitCount: 1, Status: Cancelled,
				}); err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			userID: "actor", barcode: "777777777777", direction: ptr(StockIn),
			wantID: "",
		},
		{
			name: "multiple mergeable entries: returns the one with the earliest scanned_at",
			setup: func(t *testing.T, q *Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) {
				// Insert directly, bypassing CreateScanEntry's own merge
				// check (which alone never leaves two open mergeable entries
				// for the same key), so the ORDER BY scanned_at ASC
				// tie-breaker in findMergeableScanEntry can be exercised
				// directly as a defensive measure.
				insertRawScanEntry(t, db, ScanEntry{
					ID: "scan-later", UserID: "actor", Barcode: "888888888888",
					ScannedAt: now.Add(5 * time.Minute), Direction: ptr(StockIn),
					UnitCount: 1, Status: Flagged,
				})
				insertRawScanEntry(t, db, ScanEntry{
					ID: "scan-earlier", UserID: "actor", Barcode: "888888888888",
					ScannedAt: now, Direction: ptr(StockIn),
					UnitCount: 1, Status: Pending,
				})
			},
			userID: "actor", barcode: "888888888888", direction: ptr(StockIn),
			wantID: "scan-earlier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, db, scope := newTestQueueInternal(t)
			ctx := context.Background()
			tt.setup(t, q, db, ctx, scope)

			got, err := q.findMergeableScanEntry(ctx, scope, tt.userID, tt.barcode, tt.direction)
			if err != nil {
				t.Fatalf("findMergeableScanEntry: %v", err)
			}

			if tt.wantID == "" {
				if got != nil {
					t.Errorf("want nil, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatalf("want entry %q, got nil", tt.wantID)
			}
			if got.ID != tt.wantID {
				t.Errorf("ID: want %q, got %q", tt.wantID, got.ID)
			}
		})
	}
}
