package history

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

// 00:30 on 2026-11-01 is still MDT, half an hour before the clocks fall back.
// That instant is 06:30 UTC, after 06:00 UTC, but the Denver wall time sorts
// first alphabetically.
func dstPair(t *testing.T) (later, earlier time.Time) {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later = time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	earlier = time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	if !later.After(earlier) || !later.IsDST() {
		t.Fatalf("dst fixture later=%s earlier=%s dst=%v", later, earlier, later.IsDST())
	}
	return later, earlier
}

func openHistory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Oats');
		INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPaceKeepsTheUseAcrossDST(t *testing.T) {
	later, earlier := dstPair(t)
	now := earlier.Add(60 * 24 * time.Hour)
	db := openHistory(t)
	uses := []struct {
		id string
		at time.Time
	}{
		{"dst", later},
		{"prior", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)},
		{"recent-old", now.Add(-21 * 24 * time.Hour)},
		{"recent-new", now.Add(-10 * 24 * time.Hour)},
	}
	for _, use := range uses {
		if _, err := db.Exec(
			`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES (?, 'item', ?)`,
			use.id, use.at,
		); err != nil {
			t.Fatal(err)
		}
	}
	view, err := NewJournal(db).Product(context.Background(), "item", now)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Pace.Known || view.Pace.Trend != "faster" {
		t.Fatalf("pace known=%v trend=%q days=%v", view.Pace.Known, view.Pace.Trend, view.Pace.DaysBetweenUses)
	}
}

func TestRestorePutsBackTheLaterRemovalAcrossDST(t *testing.T) {
	later, earlier := dstPair(t)
	db := openHistory(t)
	if _, err := db.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move', 'item', 'prod', 'out', 2, ?, 'manual', 0)`, earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at, removed_at, removal_reason) VALUES
			('denver', 'item', ?, ?, 'consumed'),
			('utc', 'item', ?, ?, 'consumed')`,
		earlier, later, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	n, err := restoreUnits(context.Background(), tx, moveRow{ID: "move", ItemID: "item"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restored %d", n)
	}
	var id string
	if err := tx.QueryRow(`SELECT id FROM item_instances WHERE removed_at IS NULL`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "denver" {
		t.Fatalf("restored %s, want the later Denver removal", id)
	}
}

func TestRaisingAUseTakesTheEarlierExpiryAcrossDST(t *testing.T) {
	later, earlier := dstPair(t)
	db := openHistory(t)
	if _, err := db.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move', 'item', 'prod', 'out', 1, ?, 'manual', 0)`, earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES
			('early', 'item', ?, ?),
			('late', 'item', ?, ?)`,
		earlier, earlier, earlier, later); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJournal(db).SetQuantity(context.Background(), "move", 2, earlier); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM item_instances WHERE removed_at IS NOT NULL`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "early" {
		t.Fatalf("removed %s, want the earlier UTC expiry", id)
	}
}
