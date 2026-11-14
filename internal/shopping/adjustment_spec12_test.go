package shopping

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/trip"
)

func spec12AdjSnapshot(t *testing.T, db *sql.DB, entryID string) string {
	t.Helper()
	var q int
	var prov string
	var exp, actor, setAt sql.NullString
	if err := db.QueryRow(`SELECT quantity, provider_id, expires_trip, actor_user_id, set_at
		FROM shopping_list_entry_adjustments WHERE entry_id = ?`, entryID).
		Scan(&q, &prov, &exp, &actor, &setAt); err != nil {
		return ""
	}
	return fmt.Sprintf("q=%d prov=%s exp=%v actor=%v set=%v", q, prov, exp, actor, setAt)
}

func spec12ChangeSnapshots(t *testing.T, db *sql.DB, hid string) string {
	t.Helper()
	rows, err := db.Query(`SELECT household_id, line_key, effect, cause, reason, from_qty, to_qty, n, actor_user_id, trip_seq, at
		FROM list_line_changes WHERE household_id = ? ORDER BY line_key, trip_seq, at`, hid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var hk, lk, eff, cause, reason, actor, at sql.NullString
		var fromQ, toQ, n sql.NullInt64
		var tripSeq sql.NullInt64
		if err := rows.Scan(&hk, &lk, &eff, &cause, &reason, &fromQ, &toQ, &n, &actor, &tripSeq, &at); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%v", lk.String, eff.String, cause.String, tripSeq.Int64))
	}
	return strings.Join(parts, ";")
}

func assertNewTripAddedRow(t *testing.T, db *sql.DB, hid, lineKey string, wantTrip int) {
	t.Helper()
	var effect, cause string
	var reason, actor sql.NullString
	var fromQ, toQ, n sql.NullInt64
	var tripSeq int
	if err := db.QueryRow(`SELECT effect, cause, reason, from_qty, to_qty, n, actor_user_id, trip_seq
		FROM list_line_changes WHERE household_id = ? AND line_key = ? AND trip_seq = ?`,
		hid, lineKey, wantTrip).Scan(&effect, &cause, &reason, &fromQ, &toQ, &n, &actor, &tripSeq); err != nil {
		t.Fatalf("new_trip row: %v", err)
	}
	if effect != "added" || cause != "new_trip" {
		t.Fatalf("effect/cause %q %q", effect, cause)
	}
	if reason.Valid || fromQ.Valid || toQ.Valid || n.Valid || actor.Valid {
		t.Fatalf("null cols reason=%v from=%v to=%v n=%v actor=%v", reason, fromQ, toQ, n, actor)
	}
}

// TestSpec12_04_WriteNoPrincipal — spec §12 test 4.
func TestSpec12_04_WriteNoPrincipal(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-np", "item-np")
	if err := store.SetAdjustment(context.Background(), scope, "line-np", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var actor sql.NullString
	var setAt sql.NullString
	var exp int
	if err := db.QueryRow(`SELECT actor_user_id, set_at, expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = 'line-np'`).
		Scan(&actor, &setAt, &exp); err != nil {
		t.Fatal(err)
	}
	if actor.Valid {
		t.Fatalf("actor %v", actor)
	}
	if !setAt.Valid || setAt.String == "" {
		t.Fatal("set_at missing")
	}
	var tripSeq int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq)
	if exp != tripSeq+1 {
		t.Fatalf("expires %d want %d", exp, tripSeq+1)
	}
}

// TestSpec12_03_ZeroToFiveRefreshesActor — spec §12 test 3 (0→5 path).
func TestSpec12_03_ZeroToFiveRefreshesActor(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-35", "item-35")
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-z0"})
	if err := store.SetAdjustment(ctx, scope, "line-35", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-z5"})
	time.Sleep(2 * time.Millisecond)
	if err := store.SetAdjustment(ctx, scope, "line-35", "kroger", 5); err != nil {
		t.Fatal(err)
	}
	var actor sql.NullString
	var setAt sql.NullString
	var exp sql.NullInt64
	if err := db.QueryRow(`SELECT actor_user_id, set_at, expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = 'line-35'`).
		Scan(&actor, &setAt, &exp); err != nil {
		t.Fatal(err)
	}
	if !actor.Valid || actor.String != "user-z5" {
		t.Fatalf("actor %v", actor)
	}
	if !setAt.Valid {
		t.Fatal("set_at missing")
	}
	if exp.Valid {
		t.Fatalf("expires should be null %v", exp)
	}
}

// TestSpec12_09_ActiveZeroEndTripUnfaulted — spec §12 tests 6+9: active expires_trip=trip_seq+1 lapses on unfaulted EndTrip.
func TestSpec12_09_ActiveZeroEndTripUnfaulted(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	const lineID = "line-adv9"
	const itemID = "item-adv9"
	seedAutoLine(t, db, scope, lineID, itemID)
	lineKey := ItemLineKey(itemID)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-adv9"})
	if err := store.SetAdjustment(ctx, scope, lineID, "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var tripBefore int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripBefore); err != nil {
		t.Fatal(err)
	}
	var exp int
	if err := db.QueryRow(`SELECT expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if exp != tripBefore+1 {
		t.Fatalf("expires %d want %d", exp, tripBefore+1)
	}
	adj, err := store.GetAdjustment(context.Background(), scope, lineID, "kroger")
	if err != nil || adj == nil || adj.Quantity != 0 {
		t.Fatalf("active before advance %#v err %v", adj, err)
	}
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var tripAfter int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripAfter); err != nil {
		t.Fatal(err)
	}
	if tripAfter != tripBefore+1 {
		t.Fatalf("trip_seq %d -> %d", tripBefore, tripAfter)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("zero row not deleted (count %d); <= vs < delete mutant would leave row)", n)
	}
	var chCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), lineKey).Scan(&chCount); err != nil {
		t.Fatal(err)
	}
	if chCount != 1 {
		t.Fatalf("change rows %d want 1", chCount)
	}
	assertNewTripAddedRow(t, db, scope.HouseholdID(), lineKey, tripAfter)
}

// TestSpec12_10_ActiveZeroMassStockInUnfaulted — spec §12 test 10.
func TestSpec12_10_ActiveZeroMassStockInUnfaulted(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	const lineID = "line-adv10"
	const itemID = "item-adv10"
	seedAutoLine(t, db, scope, lineID, itemID)
	lineKey := ItemLineKey(itemID)
	hid := scope.HouseholdID()
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u10"})
	if err := store.SetAdjustment(ctx, scope, lineID, "kroger", 0); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("prod-pa12-%d", i)
		iid := fmt.Sprintf("item-pa12-%d", i)
		if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, iid, hid, hid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, fmt.Sprintf("mv-pa12-%d", i), hid, iid, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	var tripBefore int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var tripAfter int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter)
	if tripAfter != tripBefore+1 {
		t.Fatalf("trip_seq %d -> %d", tripBefore, tripAfter)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&n)
	if n != 0 {
		t.Fatalf("zero row count %d", n)
	}
	assertNewTripAddedRow(t, db, hid, lineKey, tripAfter)
}

// TestSpec12_11_PurchasedLineZeroNoNewTrip — spec §12 test 11.
func TestSpec12_11_PurchasedLineZeroNoNewTrip(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	const lineID = "line-purch"
	const itemID = "item-purch"
	seedAutoLine(t, db, scope, lineID, itemID)
	lineKey := ItemLineKey(itemID)
	if _, err := db.Exec(`UPDATE shopping_list_items SET purchased_at = datetime('now') WHERE id = ?`, lineID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 1)`, scope.HouseholdID(), lineID); err != nil {
		t.Fatal(err)
	}
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&n)
	if n != 0 {
		t.Fatalf("purchased adjustment rows %d", n)
	}
	var ch int
	_ = db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ? AND cause = 'new_trip'`,
		scope.HouseholdID(), lineKey).Scan(&ch)
	if ch != 0 {
		t.Fatalf("new_trip rows for purchased line %d", ch)
	}
	t.Run("mass_stock_in_purchased_kept", func(t *testing.T) {
		store2, db2, scope2 := newTestShoppingStore(t)
		const unpurchLine, purchLine = "line-11u", "line-11p"
		const unpurchItem, purchItem = "item-11u", "item-11p"
		seedAutoLine(t, db2, scope2, unpurchLine, unpurchItem)
		seedAutoLine(t, db2, scope2, purchLine, purchItem)
		unpurchKey := ItemLineKey(unpurchItem)
		purchKey := ItemLineKey(purchItem)
		hid := scope2.HouseholdID()
		if _, err := db2.Exec(`UPDATE shopping_list_items SET purchased_at = datetime('now') WHERE id = ?`, purchLine); err != nil {
			t.Fatal(err)
		}
		ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u11"})
		if err := store2.SetAdjustment(ctx, scope2, unpurchLine, "kroger", 0); err != nil {
			t.Fatal(err)
		}
		if err := store2.SetAdjustment(ctx, scope2, purchLine, "kroger", 0); err != nil {
			t.Fatal(err)
		}
		base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
		for i := 0; i < 5; i++ {
			at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
			pid := fmt.Sprintf("prod-11-%d", i)
			iid := fmt.Sprintf("item-11x-%d", i)
			if _, err := db2.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
				t.Fatal(err)
			}
			if _, err := db2.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, iid, hid, hid, pid); err != nil {
				t.Fatal(err)
			}
			if _, err := db2.Exec(`INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
				VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, fmt.Sprintf("mv-11-%d", i), hid, iid, pid, at, at); err != nil {
				t.Fatal(err)
			}
		}
		var tripBefore int
		if err := db2.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore); err != nil {
			t.Fatal(err)
		}
		tx, err := db2.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := trip.MaybeCloseMassStockIn(context.Background(), scope2, tx, base.Add(9*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var tripAfter int
		if err := db2.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter); err != nil {
			t.Fatal(err)
		}
		if tripAfter != tripBefore+1 {
			t.Fatalf("trip_seq %d -> %d", tripBefore, tripAfter)
		}
		assertNewTripAddedRow(t, db2, hid, unpurchKey, tripAfter)
		var purchNewTrip int
		if err := db2.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ? AND cause = 'new_trip'`,
			hid, purchKey).Scan(&purchNewTrip); err != nil {
			t.Fatal(err)
		}
		if purchNewTrip != 0 {
			t.Fatalf("purchased line got %d new_trip rows; D4 mutant (drop purchased_at IS NULL) would write one", purchNewTrip)
		}
	})
}

// TestSpec12_13_NoEmptyTrip — spec §12 test 13.
func TestSpec12_13_NoEmptyTrip(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-ac5", "item-ac5")
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u-ac5"})
	closed := time.Date(2026, 11, 14, 14, 0, 0, 0, time.FixedZone("America/Denver", -7*3600))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), closed); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	snapAdj := func() string { return spec12AdjSnapshot(t, db, "line-ac5") }
	snapCh := func() string { return spec12ChangeSnapshots(t, db, scope.HouseholdID()) }
	var seqBefore int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqBefore)
	adjBefore := snapAdj()
	chBefore := snapCh()
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var seqAfter int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqAfter)
	if seqAfter != seqBefore || snapAdj() != adjBefore || snapCh() != chBefore {
		t.Fatalf("idle second EndTrip advanced seq %d->%d", seqBefore, seqAfter)
	}
	if err := store.SetAdjustment(ctx, scope, "line-ac5", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var seqAdv int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqAdv)
	if seqAdv != seqBefore+1 {
		t.Fatalf("SetAdjustment(0)+EndTrip did not advance %d->%d", seqBefore, seqAdv)
	}
	if err := store.SetAdjustment(ctx, scope, "line-ac5", "kroger", 3); err != nil {
		t.Fatal(err)
	}
	seqMid := seqAdv
	chMid := snapCh()
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var seqThree int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqThree)
	if seqThree != seqMid || snapCh() != chMid {
		t.Fatalf("0->3 before EndTrip should not advance %d->%d", seqMid, seqThree)
	}
	t.Run("denver_set_at_after_close", func(t *testing.T) {
		store2, db2, scope2 := newTestShoppingStore(t)
		seedAutoLine(t, db2, scope2, "line-den", "item-den")
		closeAt := time.Date(2026, 11, 14, 14, 0, 0, 0, time.FixedZone("America/Denver", -7*3600))
		tx2, _ := db2.BeginTx(context.Background(), nil)
		_ = trip.AdvanceTripForNewCart(context.Background(), tx2, scope2.HouseholdID(), closeAt)
		_ = tx2.Commit()
		setAt := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
		if _, err := db2.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip, set_at, actor_user_id)
			VALUES (?, 'line-den', 'kroger', 0, 3, ?, 'u-den')`, scope2.HouseholdID(), setAt.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		var seq0 int
		_ = db2.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope2.HouseholdID()).Scan(&seq0)
		if err := store2.EndTrip(context.Background(), scope2); err != nil {
			t.Fatal(err)
		}
		var seq1 int
		_ = db2.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope2.HouseholdID()).Scan(&seq1)
		if seq1 != seq0+1 {
			t.Fatalf("Denver fixture did not advance %d->%d", seq0, seq1)
		}
	})
}

// TestSpec12_08a_SetAdjustmentNoChangeRows — spec §12 test 8a.
func TestSpec12_08a_SetAdjustmentNoChangeRows(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-8a", "item-8a")
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u8a"})
	chBefore := spec12ChangeSnapshots(t, db, scope.HouseholdID())
	if err := store.SetAdjustment(ctx, scope, "line-8a", "kroger", 0); err != nil {
		t.Fatal(err)
	}
	if ch := spec12ChangeSnapshots(t, db, scope.HouseholdID()); ch != chBefore {
		t.Fatalf("SetAdjustment(0) wrote changes\nbefore %q\nafter %q", chBefore, ch)
	}
	if err := store.SetAdjustment(ctx, scope, "line-8a", "kroger", 5); err != nil {
		t.Fatal(err)
	}
	if ch := spec12ChangeSnapshots(t, db, scope.HouseholdID()); ch != chBefore {
		t.Fatalf("0->5 wrote changes\nbefore %q\nafter %q", chBefore, ch)
	}
}

// TestSpec12_08_NonZeroSurvivesBothTriggers — spec §12 test 8.
func TestSpec12_08_NonZeroSurvivesBothTriggers(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	const lineID = "line-nz8"
	seedAutoLine(t, db, scope, lineID, "item-nz8")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity)
		VALUES (?, ?, 'kroger', 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	snap := spec12AdjSnapshot(t, db, lineID)
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	if got := spec12AdjSnapshot(t, db, lineID); got != snap {
		t.Fatalf("EndTrip changed 3 row\nwas %q\ngot %q", snap, got)
	}
	base := time.Now().UTC().Add(-30 * time.Minute)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("prod-nz8-%d", i)
		iid := fmt.Sprintf("item-nz8b-%d", i)
		_, _ = db.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid)
		_, _ = db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, iid, hid, hid, pid)
		_, _ = db.Exec(`INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, fmt.Sprintf("mv-nz8-%d", i), hid, iid, pid, at, at)
	}
	tx, _ := db.BeginTx(context.Background(), nil)
	_ = trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(9*time.Minute))
	_ = tx.Commit()
	if got := spec12AdjSnapshot(t, db, lineID); got != snap {
		t.Fatalf("mass close changed 3 row\nwas %q\ngot %q", snap, got)
	}
	tx, _ = db.BeginTx(context.Background(), nil)
	if err := store.ClearAdjustmentTx(context.Background(), scope, tx, lineID, "kroger"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&n)
	if n != 0 {
		t.Fatalf("ClearAdjustmentTx left rows %d", n)
	}
}

// TestSpec12_07_LegacyZeroMatrix — spec §12 test 7 (store-level).
func TestSpec12_07_LegacyZeroMatrix(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	const zLine, tLine = "line-leg7z", "line-leg7t"
	const zItem, tItem = "item-leg7z", "item-leg7t"
	seedAutoLine(t, db, scope, zLine, zItem)
	seedAutoLine(t, db, scope, tLine, tItem)
	hid := scope.HouseholdID()
	zKey := ItemLineKey(zItem)
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES (?, 'kroger', 0, ?)`, zLine, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES (?, 'kroger', 3, ?)`, tLine, hid); err != nil {
		t.Fatal(err)
	}
	legacySnap := spec12AdjSnapshot(t, db, zLine)
	threeSnap := spec12AdjSnapshot(t, db, tLine)
	closed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	removed := closed.Add(30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-other7', 9, ?)`, hid, hid, removed); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.BeginTx(context.Background(), nil)
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	var zLeft int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, zLine).Scan(&zLeft)
	if zLeft != 0 {
		t.Fatalf("legacy zero row %d", zLeft)
	}
	assertNewTripAddedRow(t, db, hid, zKey, 1)
	if got := spec12AdjSnapshot(t, db, tLine); got != threeSnap {
		t.Fatalf("non-zero 3 changed after first advance\nwas %q\ngot %q", threeSnap, got)
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u-leg"})
	if err := store.SetAdjustment(ctx, scope, zLine, "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var exp int
	_ = db.QueryRow(`SELECT expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = ?`, zLine).Scan(&exp)
	var seq int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq)
	if exp != seq+1 {
		t.Fatalf("legacy SetAdjustment(0) expires %d want %d", exp, seq+1)
	}
	removed2 := closed.Add(2 * time.Hour).Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-other7b', 9, ?)`, hid, hid, removed2)
	tx, _ = db.BeginTx(context.Background(), nil)
	_ = trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed.Add(3*time.Hour))
	_ = tx.Commit()
	if got := spec12AdjSnapshot(t, db, tLine); got != threeSnap {
		t.Fatalf("non-zero 3 changed after second advance")
	}
	_ = legacySnap
}

// TestSpec12_07a_LegacyRowUnchangedWhenNoAdvance — spec §12 test 7a (row bytes).
func TestSpec12_07a_LegacyRowUnchangedWhenNoAdvance(t *testing.T) {
	store, db, scope := newTestShoppingStore(t)
	seedAutoLine(t, db, scope, "line-solo", "item-solo")
	closed := time.Date(2026, 11, 14, 14, 0, 0, 0, time.FixedZone("America/Denver", -7*3600))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), closed); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	removed := closed.Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-other', 3, ?)`, scope.HouseholdID(), scope.HouseholdID(), removed); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = trip.AdvanceTripForNewCart(context.Background(), tx, scope.HouseholdID(), closed.Add(2*time.Hour))
	_ = tx.Commit()
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES ('line-solo', 'kroger', 0, ?)`, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	snap := spec12AdjSnapshot(t, db, "line-solo")
	var seqBefore, changesBefore int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqBefore)
	_ = db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesBefore)
	if err := store.EndTrip(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	var seqAfter, changesAfter int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seqAfter)
	_ = db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesAfter)
	if seqAfter != seqBefore || changesAfter != changesBefore {
		t.Fatalf("legacy-only advanced seq %d->%d changes %d->%d", seqBefore, seqAfter, changesBefore, changesAfter)
	}
	if got := spec12AdjSnapshot(t, db, "line-solo"); got != snap {
		t.Fatalf("legacy row changed %q -> %q", snap, got)
	}
}

// TestSpec12_07b_NoSecondNewTripRow — spec §12 test 7b.
func TestSpec12_07b_NoSecondNewTripRow(t *testing.T) {
	_, db, scope := newTestShoppingStore(t)
	const itemID = "item-7b"
	seedAutoLine(t, db, scope, "line-7b", itemID)
	hid := scope.HouseholdID()
	key := ItemLineKey(itemID)
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id)
		VALUES ('line-7b', 'kroger', 0, ?)`, hid); err != nil {
		t.Fatal(err)
	}
	closed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	removed := closed.Add(30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-x7b', 9, ?)`, hid, hid, removed); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertNewTripAddedRow(t, db, hid, key, 1)
	var zLeft int
	_ = db.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = 'line-7b'`).Scan(&zLeft)
	if zLeft != 0 {
		t.Fatalf("legacy zero row %d", zLeft)
	}
	removed2 := closed.Add(2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-y7b', 9, ?)`, hid, hid, removed2); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var tripSeq int
	_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq)
	if tripSeq != 2 {
		t.Fatalf("trip_seq %d want 2", tripSeq)
	}
	var atT2 int
	_ = db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ? AND cause = 'new_trip' AND trip_seq = ?`,
		hid, key, tripSeq).Scan(&atT2)
	if atT2 != 0 {
		t.Fatalf("T+2 advance wrote new_trip for line (trip_seq=%d count %d)", tripSeq, atT2)
	}
}

// TestSpec12_17_CrossHouseholdBNoChangeRow — spec §12 test 17 B change row.
func TestSpec12_17_CrossHouseholdBNoChangeRow(t *testing.T) {
	storeA, db, scopeA := newTestShoppingStore(t)
	seedAutoLine(t, db, scopeA, "line-a17", "item-a17")
	const otherHouse = "0190bbbb-cccc-7000-8000-0000000000e5"
	const zLine = "line-b17"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary) VALUES (?, 'B', '', datetime('now'), 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 0)`, otherHouse); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source) VALUES (?, ?, ?, 'item-b17', 1, 'auto')`,
		zLine, otherHouse, otherHouse); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity, household_id, expires_trip)
		VALUES (?, 'kroger', 0, ?, 1)`, zLine, otherHouse); err != nil {
		t.Fatal(err)
	}
	bAdjBefore := spec12AdjSnapshot(t, db, zLine)
	removed := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = db.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-x17', 5, ?)`, scopeA.HouseholdID(), scopeA.HouseholdID(), removed)
	if err := storeA.EndTrip(context.Background(), scopeA); err != nil {
		t.Fatal(err)
	}
	var bCh int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, otherHouse).Scan(&bCh); err != nil {
		t.Fatal(err)
	}
	if bCh != 0 {
		t.Fatalf("household B got %d change rows", bCh)
	}
	bAdjAfter := spec12AdjSnapshot(t, db, zLine)
	if bAdjAfter != bAdjBefore || bAdjAfter == "" {
		t.Fatalf("household B adjustment changed\nbefore %q\nafter %q (D5 mutant would delete cross-household row)", bAdjBefore, bAdjAfter)
	}
}