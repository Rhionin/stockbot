package trip_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/Rhionin/stockbot/internal/trip"
)

func TestAdvanceSeqIncrementsTrip(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	closed := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := trip.AdvanceSeq(context.Background(), tx, scope.HouseholdID(), trip.CloseCauseNewCart, closed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	var cause sql.NullString
	var at sql.NullString
	household := scope.HouseholdID()
	if err := conn.QueryRow(`SELECT trip_seq, trip_closed_cause, trip_closed_at FROM households WHERE id = ?`, household).
		Scan(&seq, &cause, &at); err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("trip_seq: %d want 1", seq)
	}
	if !cause.Valid || cause.String != trip.CloseCauseNewCart {
		t.Fatalf("trip_closed_cause: %v", cause)
	}
	if !at.Valid || at.String != closed.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("trip_closed_at: %v", at)
	}
}

func TestPurgeExpiredSkipsRemovesOutlivedRows(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	if _, err := conn.Exec(`UPDATE households SET trip_seq = 2 WHERE id = ?`, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'gone', 2)`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'stay', 3)`, household, household); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.PurgeExpiredSkips(context.Background(), tx, household); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM staged_cart_skips WHERE household_id = ?`, household).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("skips after purge: %d", n)
	}
}

func TestMaybeCloseMassStockInAdvancesTrip(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	base := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := "prod-" + string(rune('a'+i))
		if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
			"item-"+pid, household, household, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
			"move-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("trip_seq after mass close: %d", seq)
	}
}

func TestMaybeCloseMassStockInSkipsWhenPendingStockInScans(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	if _, err := conn.Exec(`
		INSERT INTO scan_entries (id, household_id, user_id, barcode, direction, status, scanned_at)
		VALUES ('pending-in', ?, ?, '111', 'stock_in', 'pending', '2026-01-01T00:00:00Z')`,
		household, household); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("trip_seq should not advance with pending scans: %d", seq)
	}
}

func TestAdvanceSeqRejectsEmptyHousehold(t *testing.T) {
	err := trip.AdvanceSeq(context.Background(), nil, "", trip.CloseCauseNewCart, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAdvanceSeqRejectsUnknownCause(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = trip.AdvanceSeq(context.Background(), tx, scope.HouseholdID(), "bogus", time.Now().UTC())
	if err == nil {
		t.Fatal("expected unknown cause error")
	}
}

func TestMaybeCloseMassStockInNoOpsWithoutMoves(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMaybeCloseMassStockInIdempotentClosure(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	base := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("idem-%d", i)
		if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
			"item-"+pid, household, household, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
			"mv-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(10 * time.Minute)
	for i := 0; i < 2; i++ {
		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, now); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("trip_seq after duplicate close: %d", seq)
	}
}

func TestAdvanceSeqUnknownHousehold(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = trip.AdvanceSeq(context.Background(), tx, "00000000-0000-0000-0000-000000000099", trip.CloseCauseNewCart, time.Now().UTC())
	if err == nil {
		t.Fatal("expected missing household")
	}
}

func TestHouseholdTripClosedNullBeforeAdvance(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	var cause sql.NullString
	var at sql.NullString
	if err := conn.QueryRow(`SELECT trip_closed_cause, trip_closed_at FROM households WHERE id = ?`, scope.HouseholdID()).
		Scan(&cause, &at); err != nil {
		t.Fatal(err)
	}
	if cause.Valid || at.Valid {
		t.Fatalf("before trip: cause %v at %v", cause, at)
	}
}

func TestMaybeCloseMassStockInSetsPutAwayCause(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	base := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("putaway-%d", i)
		if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
			"item-"+pid, household, household, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
			"mv-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(10 * time.Minute)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var cause sql.NullString
	var at sql.NullString
	if err := conn.QueryRow(`SELECT trip_closed_cause, trip_closed_at FROM households WHERE id = ?`, household).
		Scan(&cause, &at); err != nil {
		t.Fatal(err)
	}
	if !cause.Valid || cause.String != trip.CloseCausePutAway {
		t.Fatalf("cause: %v", cause)
	}
	if !at.Valid || at.String != now.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("closed at: %v", at)
	}
}

func TestMaybeCloseMassStockInTooFewProducts(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	base := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("few-%d", i)
		if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
			"item-"+pid, household, household, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
			"mv-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("trip_seq should not advance for <5 products: %d", seq)
	}
}

func TestMaybeCloseMassStockInBeforeClusterClose(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	base := time.Date(2026, 4, 4, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("wait-%d", i)
		if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
			"item-"+pid, household, household, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
			"mv-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("trip_seq should not advance before cluster close window: %d", seq)
	}
}

func TestMaybeCloseMassStockInIgnoresOlderCluster(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	oldBase := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	newBase := time.Date(2026, 4, 5, 8, 0, 0, 0, time.UTC)
	insertCluster := func(base time.Time, prefix string, n int) {
		for i := 0; i < n; i++ {
			at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
			pid := fmt.Sprintf("%s-%d", prefix, i)
			if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
				"item-"+pid, household, household, pid); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`
				INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
				VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`,
				"mv-"+pid, household, "item-"+pid, pid, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertCluster(oldBase, "old", 5)
	insertCluster(newBase, "new", 2)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, newBase.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 0 {
		t.Fatalf("only last cluster counts; <5 in last cluster: %d", seq)
	}
}

func TestMaybeCloseMassStockInRejectsBadStockInTime(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('bad-time', 'bad', 't')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-bad', ?, ?, 'bad-time')`,
		household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		VALUES ('mv-bad', ?, 'item-bad', 'bad-time', 'in', 1, 'not-a-time', 'scan', 1, 'not-a-time')`,
		household); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = trip.MaybeCloseMassStockIn(context.Background(), scope, tx, time.Now().UTC())
	if err == nil {
		t.Fatal("expected bad time error")
	}
}

func TestMaybeCloseMassStockInRejectsMissingScope(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = trip.MaybeCloseMassStockIn(context.Background(), tenant.Scope{}, tx, time.Now().UTC())
	if err == nil {
		t.Fatal("expected missing household error")
	}
	_ = err
}
