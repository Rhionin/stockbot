package server

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestApplyZeroForTripEarlyExit(t *testing.T) {
	items := []ShoppingListEntryResponse{{ID: "l1", ItemID: "i1", CanSendToCart: true, Adjustment: intPtr(0)}}
	off := []OffListRowResponse{{ID: "item:i1"}}
	gotItems, gotOff, err := applyZeroForTrip(context.Background(), nil, items, off, nil, nil, nil, nil, "", nil, tenant.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotItems) != 1 || len(gotOff) != 1 {
		t.Fatalf("no provider/items unchanged: items %d off %d", len(gotItems), len(gotOff))
	}
}

func TestApplyZeroForTripMovesActiveZeroToOffList(t *testing.T) {
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
	hid := scope.HouseholdID()
	const itemID = "item-zft-asm"
	const lineID = "line-zft-asm"
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('prod-zft', 'Z', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'prod-zft')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	const actor = "user-zft"
	if _, err := conn.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, created_at) VALUES (?, 'Zelda', 'zft@test', 'x', datetime('now'))`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, actor_user_id, set_at, expires_trip)
		VALUES (?, ?, 'kroger', 0, ?, ?, 5)`, hid, lineID, actor, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	key := shopping.ItemLineKey(itemID)
	adjZero := 0
	items := []ShoppingListEntryResponse{{
		ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adjZero,
	}}
	plan := map[string]supply.LinePlanView{key: {ProductID: "prod-zft", ItemID: itemID}}
	names := map[string]string{"prod-zft": "Z Product"}
	kept, off, err := applyZeroForTrip(context.Background(), conn, items, nil, nil, plan, names, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Fatalf("kept %d want 0", len(kept))
	}
	if len(off) != 1 || off[0].OffListReason != "zero_for_trip" || off[0].ZeroForTrip == nil {
		t.Fatalf("offList %#v", off)
	}
	if off[0].ZeroForTrip.ActorDisplayName == nil || *off[0].ZeroForTrip.ActorDisplayName != "Zelda" {
		t.Fatalf("actor name %#v", off[0].ZeroForTrip.ActorDisplayName)
	}
}

func TestApplyZeroForTripSkipsStagedLine(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-skip"
	const lineID = "line-zft-skip"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p', 'P', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj}}
	skipped := map[string]struct{}{key: {}}
	kept, off, err := applyZeroForTrip(context.Background(), conn, items, nil, skipped, map[string]supply.LinePlanView{key: {}},
		map[string]string{"p": "P"}, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || len(off) != 0 {
		t.Fatalf("skipped zero stays on list: kept %d off %d", len(kept), len(off))
	}
}

func TestApplyZeroForTripTagsExistingOffRow(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-off"
	const lineID = "line-zft-off"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p2', 'P2', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p2')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj}}
	off := []OffListRowResponse{{ID: key, OffListReason: "taken_off", Name: "P2"}}
	kept, out, err := applyZeroForTrip(context.Background(), conn, items, off, nil, map[string]supply.LinePlanView{key: {ProductID: "p2"}},
		map[string]string{"p2": "P2"}, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 || len(out) != 1 {
		t.Fatalf("kept %d off %d", len(kept), len(out))
	}
	if out[0].OffListReason != "zero_for_trip" || out[0].ZeroForTrip == nil {
		t.Fatalf("off row %#v", out[0])
	}
}

func TestApplyZeroForTripRemovedOffRowUntouched(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-rem"
	const lineID = "line-zft-rem"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p3', 'P3', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p3')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj}}
	off := []OffListRowResponse{{ID: key, OffListReason: "removed", Name: "P3"}}
	kept, out, err := applyZeroForTrip(context.Background(), conn, items, off, nil, map[string]supply.LinePlanView{key: {}},
		nil, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 || len(out) != 1 || out[0].OffListReason != "removed" {
		t.Fatalf("removed row kept: items %d off %#v", len(kept), out)
	}
}

func TestActiveZeroForTripKeys(t *testing.T) {
	z := 0
	keys := activeZeroForTripKeys([]ShoppingListEntryResponse{
		{LineKey: "item:a", Adjustment: &z, CanSendToCart: true},
		{LineKey: "item:b", CanSendToCart: true},
		{LineKey: "item:c", Adjustment: &z, CanSendToCart: false},
	})
	if len(keys) != 1 {
		t.Fatalf("keys %d", len(keys))
	}
	if _, ok := keys["item:a"]; !ok {
		t.Fatal("missing item:a")
	}
}

func TestApplyZeroForTripFallsBackToLineName(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-name"
	const lineID = "line-zft-name"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p-hand', 'P', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p-hand')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'manual', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{
		ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj,
		LineDisplayName: "Hand Added",
	}}
	kept, off, err := applyZeroForTrip(context.Background(), conn, items, nil, nil,
		map[string]supply.LinePlanView{key: {ItemID: itemID}}, nil, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Fatalf("kept %d", len(kept))
	}
	if len(off) != 1 || off[0].Name != "Hand Added" {
		t.Fatalf("off %#v", off)
	}
}

func TestApplyZeroForTripUnnamedStaysOnList(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-noname"
	const lineID = "line-zft-noname"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p-noname', '', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p-noname')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'manual', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{
		ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj,
	}}
	kept, off, err := applyZeroForTrip(context.Background(), conn, items, nil, nil,
		map[string]supply.LinePlanView{key: {ItemID: itemID}}, map[string]string{}, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || len(off) != 0 {
		t.Fatalf("unnamed stays on list: kept %d off %d", len(kept), len(off))
	}
	var qty int
	if err := conn.QueryRow(`SELECT quantity FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&qty); err != nil || qty != 0 {
		t.Fatalf("adj row changed qty=%d err=%v", qty, err)
	}
}

func TestApplyZeroForTripMemberNameFallback(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), conn)
	hid := scope.HouseholdID()
	const itemID = "item-zft-mem"
	const lineID = "line-zft-mem"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p-mem', '', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p-mem')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'manual', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, 3)`, hid, lineID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{
		ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj,
		Group: &ShoppingListGroupResponse{
			Members: []ShoppingListGroupMemberResponse{{ItemID: itemID, Name: "Member Label"}},
		},
	}}
	kept, off, err := applyZeroForTrip(context.Background(), conn, items, nil, nil,
		map[string]supply.LinePlanView{key: {ItemID: itemID}}, map[string]string{}, nil, "kroger", store, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 || len(off) != 1 || off[0].Name != "Member Label" {
		t.Fatalf("kept %d off %#v", len(kept), off)
	}
}

func TestApplyZeroForTripPropagatesGetAdjustmentError(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	const itemID = "item-zft-err"
	const lineID = "line-zft-err"
	key := shopping.ItemLineKey(itemID)
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p-err', 'P', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p-err')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(conn)
	adj := 0
	items := []ShoppingListEntryResponse{{ID: lineID, ItemID: itemID, LineKey: key, CanSendToCart: true, Adjustment: &adj}}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = applyZeroForTrip(context.Background(), conn, items, nil, nil, map[string]supply.LinePlanView{key: {}},
		nil, nil, "kroger", store, scope)
	if err == nil {
		t.Fatal("expected GetAdjustment error")
	}
}

func TestZeroForTripOffListNameFallbacks(t *testing.T) {
	key := "item:i1"
	if got := zeroForTripOffListName(ShoppingListEntryResponse{LineDisplayName: "Hand Added"}, supply.LinePlanView{ItemID: "i1"}, nil, nil, key); got != "Hand Added" {
		t.Fatalf("line display %q", got)
	}
	if got := zeroForTripOffListName(ShoppingListEntryResponse{
		ItemID: "i1",
		Group:  &ShoppingListGroupResponse{ID: "g1", Name: "GP"},
	}, supply.LinePlanView{ItemID: "i1", GroupID: "g1"}, nil, map[string]string{"g1": "GP"}, key); got != "GP" {
		t.Fatalf("group name %q", got)
	}
	if got := zeroForTripOffListName(ShoppingListEntryResponse{ItemID: "i1"}, supply.LinePlanView{ItemID: "i1"}, nil, nil, key); got != "" {
		t.Fatalf("unnamed %q", got)
	}
}

func TestWireZeroForTripActorDisplay(t *testing.T) {
	actor := "u1"
	adj := &shopping.Adjustment{
		ActorUserID: &actor,
		SetAt:       func() *time.Time { t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); return &t }(),
	}
	z := wireZeroForTrip("line", "kroger", adj, map[string]string{actor: "Ada"})
	if z == nil || z.ActorDisplayName == nil || *z.ActorDisplayName != "Ada" {
		t.Fatalf("wire %#v", z)
	}
}

func intPtr(n int) *int { return &n }