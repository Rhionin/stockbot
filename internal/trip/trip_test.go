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
	wantClose := base.Add(4*time.Minute + clusterCloseDuration())
	got, err := time.Parse(time.RFC3339Nano, at.String)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Valid || !got.Equal(wantClose) {
		t.Fatalf("closed at: %v want %v", at, wantClose)
	}
}

func clusterCloseDuration() time.Duration {
	return 5 * time.Minute
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

func TestMaybeCloseMassStockInClosesEveryQualifyingCluster(t *testing.T) {
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
	closeAt := oldBase.Add(9 * time.Minute)
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, closeAt); err != nil {
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
		t.Fatalf("old cluster should close trip: %d", seq)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM stock_in_clusters WHERE household_id = ?`, household).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("recorded clusters: %d", n)
	}
}

func TestPutAwayNoEmptyTripSecondCloseWithoutSkips(t *testing.T) {
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
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := "p" + string(rune('a'+i))
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
	firstClose := base.Add(9 * time.Minute)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, firstClose); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	secondClose := firstClose.Add(time.Hour)
	tx, err = conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, secondClose); err != nil {
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
		t.Fatalf("second close without skips should not advance: %d", seq)
	}
}

func TestClusterMinReadFromHouseholdColumn(t *testing.T) {
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
	if _, err := conn.Exec(`UPDATE households SET stock_in_cluster_min = 3 WHERE id = ?`, household); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := "k" + string(rune('a'+i))
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
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var kUsed int
	if err := conn.QueryRow(`SELECT k_used FROM stock_in_clusters WHERE household_id = ?`, household).Scan(&kUsed); err != nil {
		t.Fatal(err)
	}
	if kUsed != 3 {
		t.Fatalf("k_used: %d", kUsed)
	}
}

func TestManualStockInDoesNotCountTowardCluster(t *testing.T) {
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
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := "s" + string(rune('a'+i))
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
	manualAt := base.Add(4 * time.Minute).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		VALUES ('mv-manual', ?, 'item-sa', 'sa', 'in', 1, ?, 'manual', 1, ?)`,
		household, manualAt, manualAt); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(20*time.Minute)); err != nil {
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
		t.Fatalf("only four scan products, manual ignored: trip_seq %d", seq)
	}
}

func TestRepeatScanSameProductCountsOnceForK(t *testing.T) {
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
	if _, err := conn.Exec(`UPDATE households SET stock_in_cluster_min = 3 WHERE id = ?`, household); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	pids := []string{"r1", "r2", "r3"}
	for i, pid := range pids {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
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
			"mv1-"+pid, household, "item-"+pid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	// repeat scan same product
	repeatAt := base.Add(3 * time.Minute).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		VALUES ('mv2-r1', ?, 'item-r1', 'r1', 'in', 1, ?, 'scan', 1, ?)`,
		household, repeatAt, repeatAt); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(`SELECT product_count FROM stock_in_clusters WHERE household_id = ?`, household).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("product_count: %d", count)
	}
}

func TestAdvanceSeqClearsStaleListLineChanges(t *testing.T) {
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
		INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:old', 'taken_off', 'list_updated', '2026-01-01T00:00:00Z', 0)`, household); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceSeq(context.Background(), tx, household, trip.CloseCauseNewCart, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, household).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("stale list_line_changes: %d", n)
	}
}

func TestNewCartAdvanceAfterZeroAdjustment(t *testing.T) {
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
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, household, closed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source)
		VALUES ('entry-adj', ?, ?, 'item-adj', 1, 'auto')`, household, household); err != nil {
		t.Fatal(err)
	}
	setAt := closed.Add(time.Minute).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
		INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id, set_at, expires_trip)
		VALUES ('entry-adj', 'kroger', 0, ?, ?, 2)`, household, setAt); err != nil {
		t.Fatal(err)
	}
	tx, err = conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, household, closed.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("adjustment should allow second advance, got %d", seq)
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
