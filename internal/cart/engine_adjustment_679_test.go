package cart

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
)

// TestSpec12_05_06_GetComputedEntriesZeroEdges — spec §12 tests 5–6 cart send quantity.
func TestSpec12_05_06_GetComputedEntriesZeroEdges(t *testing.T) {
	env := newProvisionEnv(t)
	const barcode = "8400000006791"
	itemID, entryID := env.seedShortfall(t, "prod-679-send", "Send Edge", barcode, 4)
	env.connect(t, ProviderID("kroger"))
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "u-send"})
	if err := env.shoppingList.SetAdjustment(ctx, env.scope, entryID, "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var tripSeq int
	if err := env.db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, env.scope.HouseholdID()).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	var exp int
	if err := env.db.QueryRow(`SELECT expires_trip FROM shopping_list_entry_adjustments WHERE entry_id = ?`, entryID).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if exp != tripSeq+1 {
		t.Fatalf("expires %d want %d", exp, tripSeq+1)
	}
	entries, err := env.engine.getComputedEntries(ctx, env.scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.EntryID == entryID {
			t.Fatalf("active zero should send qty 0 (line omitted); got entry qty %d", e.Quantity)
		}
	}
	_ = itemID
	if _, err := env.db.Exec(`UPDATE shopping_list_entry_adjustments SET expires_trip = ? WHERE entry_id = ?`, tripSeq, entryID); err != nil {
		t.Fatal(err)
	}
	adj, err := env.shoppingList.GetAdjustment(ctx, env.scope, entryID, "kroger")
	if err != nil || adj != nil {
		t.Fatalf("expired read %#v err %v", adj, err)
	}
	entries, err = env.engine.getComputedEntries(ctx, env.scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.EntryID == entryID {
			found = true
			if e.Quantity != 4 {
				t.Fatalf("expired zero send qty %d want staged 4", e.Quantity)
			}
		}
	}
	if !found {
		t.Fatal("staged line missing from cart after expired zero")
	}
}