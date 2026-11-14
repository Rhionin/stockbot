package shopping

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/Rhionin/stockbot/internal/trip"
)

func newTestShoppingStore(t *testing.T) (*Store, *sql.DB, tenant.Scope) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(conn), conn, scope
}

func seedAutoLine(t *testing.T, db *sql.DB, scope tenant.Scope, lineID, itemID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, 'p', 'c')`, "prod-"+itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`,
		itemID, scope.HouseholdID(), scope.HouseholdID(), "prod-"+itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source)
		VALUES (?, ?, ?, ?, 2, 'auto')`, lineID, scope.HouseholdID(), scope.HouseholdID(), itemID); err != nil {
		t.Fatal(err)
	}
}

func TestSetAdjustment679WriteAtZero(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-z", "item-z")
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-679"})
	before := time.Now().Add(-time.Minute)
	if err := store.SetAdjustment(ctx, scope, "line-z", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var qty, exp int
	var actor, setAt sql.NullString
	if err := db.QueryRow(`SELECT quantity, expires_trip, actor_user_id, set_at FROM shopping_list_entry_adjustments WHERE entry_id = ?`,
		"line-z").Scan(&qty, &exp, &actor, &setAt); err != nil {
		t.Fatal(err)
	}
	var tripSeq int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq)
	if qty != 0 || exp != tripSeq+1 {
		t.Fatalf("qty/exp %d %d", qty, exp)
	}
	if !actor.Valid || actor.String != "user-679" {
		t.Fatalf("actor %v", actor)
	}
	ts, err := trip.ParseTime(setAt.String)
	if err != nil || ts.Before(before) {
		t.Fatalf("set_at %v err %v", setAt, err)
	}
}

func TestSetAdjustment679UpdatePaths(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-u", "item-u")
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-a"})
	if err := store.SetAdjustment(ctx, scope, "line-u", "kroger", 3); err != nil {
		t.Fatal(err)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-b"})
	if err := store.SetAdjustment(ctx, scope, "line-u", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var exp1 int
	var set1 sql.NullString
	if err := db.QueryRow(`SELECT expires_trip, set_at FROM shopping_list_entry_adjustments WHERE entry_id = ?`, "line-u").
		Scan(&exp1, &set1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-c"})
	if err := store.SetAdjustment(ctx, scope, "line-u", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var exp2 int
	var actor sql.NullString
	var set2 sql.NullString
	if err := db.QueryRow(`SELECT expires_trip, actor_user_id, set_at FROM shopping_list_entry_adjustments WHERE entry_id = ?`, "line-u").
		Scan(&exp2, &actor, &set2); err != nil {
		t.Fatal(err)
	}
	if exp1 != exp2 {
		t.Fatalf("expires_trip changed %d -> %d", exp1, exp2)
	}
	if !actor.Valid || actor.String != "user-c" || set2.String == set1.String {
		t.Fatalf("refresh actor/set_at %v %q %q", actor, set1.String, set2.String)
	}
	if err := store.SetAdjustment(ctx, scope, "line-u", "kroger", 5); err != nil {
		t.Fatal(err)
	}
	var exp3 sql.NullInt64
	if err := db.QueryRow(`SELECT expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = ?`, "line-u").Scan(&exp3); err != nil {
		t.Fatal(err)
	}
	if exp3.Valid {
		t.Fatalf("non-zero should clear expires_trip %v", exp3)
	}
}

func TestGetAdjustment679ActiveExpiredEdges(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-e", "item-e")
	var tripSeq int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, 'line-e', 'kroger', 0, ?)`, scope.HouseholdID(), tripSeq+1); err != nil {
		t.Fatal(err)
	}
	adj, err := store.GetAdjustment(context.Background(), scope, "line-e", "kroger")
	if err != nil || adj == nil || adj.Quantity != 0 {
		t.Fatalf("active %#v err %v", adj, err)
	}
	if _, err := db.Exec(`UPDATE shopping_list_entry_adjustments SET expires_trip = ? WHERE entry_id = ?`, tripSeq, "line-e"); err != nil {
		t.Fatal(err)
	}
	adj, err = store.GetAdjustment(context.Background(), scope, "line-e", "kroger")
	if err != nil || adj != nil {
		t.Fatalf("expired should be nil, got %#v err %v", adj, err)
	}
}

func TestLegacyZeroLapsesOnAdvance(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-leg", "item-leg")
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES ('line-leg', 'kroger', 0, ?)`, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	adj, err := store.GetAdjustment(context.Background(), scope, "line-leg", "kroger")
	if err != nil || adj == nil || adj.Quantity != 0 {
		t.Fatalf("legacy active %#v", adj)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = 'line-leg'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("legacy zero row count %d", n)
	}
	var ch int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), ItemLineKey("item-leg")).Scan(&ch); err != nil {
		t.Fatal(err)
	}
	if ch != 1 {
		t.Fatalf("new_trip change rows %d", ch)
	}
}

func TestLegacyZeroAloneDoesNotAdvance(t *testing.T) {
	_, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-solo", "item-solo")
	closed := time.Date(2026, 11, 14, 14, 0, 0, 0, time.FixedZone("America/Denver", -7*3600))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), closed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	removed := closed.Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-other', 3, ?)`, scope.HouseholdID(), scope.HouseholdID(), removed); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), closed.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES ('line-solo', 'kroger', 0, ?)`, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	var seqBefore, changesBefore int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	if err := storeEndTripOnly(context.Background(), db, scope); err != nil {
		t.Fatal(err)
	}
	var seqAfter, changesAfter int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqAfter)
	_ = db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesAfter)
	if seqAfter != seqBefore || changesAfter != changesBefore {
		t.Fatalf("legacy-only should not advance seq %d->%d changes %d->%d", seqBefore, seqAfter, changesBefore, changesAfter)
	}
}

func storeEndTripOnly(ctx context.Context, db *sql.DB, scope tenant.Scope) error {
	store := NewStore(db)
	return store.EndTrip(ctx, scope)
}

func TestCrossHouseholdAdjustmentIsolation(t *testing.T) {
	storeA, db, scopeA := newTestShoppingStore(t)
	seedAutoLine(t, db, scopeA, "line-a", "item-a")
	const otherHouse = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary) VALUES (?, 'B', '', datetime('now'), 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 0)`, otherHouse); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source) VALUES ('line-b', ?, ?, 'item-b', 1, 'auto')`,
		otherHouse, otherHouse); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id, expires_trip)
		VALUES ('line-b', 'kroger', 0, ?, 5)`, otherHouse); err != nil {
		t.Fatal(err)
	}
	scopeB, _ := tenant.New(otherHouse)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scopeA.HouseholdID(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	var bRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE household_id = ?`, otherHouse).Scan(&bRows)
	if bRows != 1 {
		t.Fatalf("household B row count %d", bRows)
	}
	err = storeA.SetAdjustment(context.Background(), scopeA, "line-b", "kroger", 0)
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("foreign entry set err %v want ErrItemNotFound", err)
	}
	_ = scopeB
}