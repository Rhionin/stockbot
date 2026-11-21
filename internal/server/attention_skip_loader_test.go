package server

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
)

func TestLoadActiveSkipsExpiryEdges(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	const tripSeq = 7
	if _, err := db.Exec(`UPDATE households SET trip_seq = ? WHERE id = ?`, tripSeq, hid); err != nil {
		t.Fatal(err)
	}
	itemT := "item-skip-t"
	itemTP := "item-skip-tp1"
	for _, spec := range []struct {
		item, expires string
	}{
		{itemT, "7"},
		{itemTP, "8"},
	} {
		if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, 'P', 'c')`, "p-"+spec.item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, spec.item, hid, hid, "p-"+spec.item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO staged_cart_skips (user_id, household_id, item_id, expires_trip, actor_user_id)
			VALUES (?, ?, ?, ?, ?)`, hid, hid, spec.item, spec.expires, hid); err != nil {
			t.Fatal(err)
		}
	}
	skips, err := batchActiveSkips(context.Background(), db, scope)
	if err != nil {
		t.Fatal(err)
	}
	keyT := shopping.ItemLineKey(itemT)
	keyTP := shopping.ItemLineKey(itemTP)
	if _, ok := skips[keyT]; ok {
		t.Fatal("expires_trip = T must not be skipped")
	}
	if _, ok := skips[keyTP]; !ok {
		t.Fatal("expires_trip = T+1 must be skipped")
	}
	sendFail := []attention.SendResultRow{{ItemID: itemT, Status: "failed", Reason: "api_error", AttemptedAt: "2026-06-01T00:00:00Z"}}
	in := attention.EvaluateInput{
		TripSeq: tripSeq, Marks: map[string]attention.MarkRow{},
		SendByLine: map[string][]attention.SendResultRow{
			keyT:  sendFail,
			keyTP: {{ItemID: itemTP, Status: "failed", Reason: "api_error", AttemptedAt: "2026-06-01T00:00:00Z"}},
		},
		Skipped: skips, ActiveZero: map[string]struct{}{},
		PlanViews: map[string]supply.LinePlanView{}, UnpurchasedStaged: map[string]int{
			keyT: 1, keyTP: 1,
		},
		PlanViewEntryByKey: map[string]attention.PlanEntryIDs{},
	}
	perLine, _ := attention.Evaluate(in)
	if perLine[keyTP] != nil {
		t.Fatalf("skipped line flagged %#v", perLine[keyTP])
	}
	if perLine[keyT] == nil || perLine[keyT].Reason != attention.ReasonSendFailed {
		t.Fatalf("non-skipped line %#v", perLine[keyT])
	}
}