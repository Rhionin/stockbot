package attention

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestLoadMarks(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, 'item:x', 1)`, hid); err != nil {
		t.Fatal(err)
	}
	marks, err := LoadMarks(context.Background(), db, scope)
	if err != nil || marks["item:x"].FlaggedTrip != 1 {
		t.Fatalf("marks %#v err %v", marks, err)
	}
}

func TestLoadSendResultsRetentionInclusiveAtCutoff(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	cutoff := now.Add(-cart.SendResultRetention)
	atEdge := cutoff.UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, attempted_at) VALUES (?, 'kroger', 'item-y', 'failed', 'api_error', ?)`, hid, atEdge); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadSendResults(context.Background(), db, scope, "kroger", cutoff)
	if err != nil || len(rows) != 1 {
		t.Fatalf("edge row must load: %#v err %v", rows, err)
	}
}

func TestLoadSendResultsRetentionExclusiveBeforeCutoff(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	cutoff := now.Add(-cart.SendResultRetention)
	if _, err := db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, attempted_at) VALUES (?, 'kroger', 'item-y', 'failed', 'api_error', ?)`,
		hid, cutoff.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	old := cutoff.Add(-time.Second).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, attempted_at) VALUES (?, 'kroger', 'item-z', 'failed', 'api_error', ?)`, hid, old); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadSendResults(context.Background(), db, scope, "kroger", cutoff)
	if err != nil || len(rows) != 1 || rows[0].ItemID != "item-y" {
		t.Fatalf("cutoff - 1s excluded: %#v err %v", rows, err)
	}
}

func TestLoadActiveZeroExpiryEdges(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, _ := auth.PrimaryScope(context.Background(), db)
	hid := scope.HouseholdID()
	const tripSeq = 5
	if _, err := db.Exec(`UPDATE households SET trip_seq = ? WHERE id = ?`, tripSeq, hid); err != nil {
		t.Fatal(err)
	}
	lineT := "line-t"
	lineTP := "line-tp1"
	lineNULL := "line-null"
	lineNonZero := "line-nz"
	itemT := "item-t"
	itemTP := "item-tp"
	itemNULL := "item-null"
	itemNZ := "item-nz"
	for _, row := range []struct{ line, item, exp string }{
		{lineT, itemT, "5"},
		{lineTP, itemTP, "6"},
		{lineNULL, itemNULL, "NULL"},
	} {
		if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, 'P', 'c')`, "p-"+row.item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, row.item, hid, hid, "p-"+row.item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
			VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, row.line, hid, hid, row.item); err != nil {
			t.Fatal(err)
		}
		if row.exp == "NULL" {
			if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
				VALUES (?, ?, 'kroger', 0, NULL)`, hid, row.line); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
				VALUES (?, ?, 'kroger', 0, ?)`, hid, row.line, row.exp); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES ('p-item-nz', 'P', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'p-item-nz')`, itemNZ, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineNonZero, hid, hid, itemNZ); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 2, NULL)`, hid, lineNonZero); err != nil {
		t.Fatal(err)
	}
	active, err := LoadActiveZero(context.Background(), db, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := active[shopping.ItemLineKey(itemT)]; ok {
		t.Fatal("expires_trip = T must not be active zero")
	}
	for _, key := range []string{shopping.ItemLineKey(itemTP), shopping.ItemLineKey(itemNULL)} {
		if _, ok := active[key]; !ok {
			t.Fatalf("missing active zero %s", key)
		}
	}
	if _, ok := active[shopping.ItemLineKey(itemNZ)]; ok {
		t.Fatal("non-zero quantity must not be active zero")
	}
}