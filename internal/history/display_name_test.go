package history

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestUndoUsesTheHouseholdName(t *testing.T) {
	db := openHistoryDB(t)
	ctx := context.Background()
	primary := scopeOf(t, db)
	const cedar = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedar); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(cedar)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO history_repair (household_id, id) VALUES (?, 1)`, cedar); err != nil {
		t.Fatal(err)
	}
	catalog := product.NewCatalog(db)
	const typed = "cedar-sentinel-q9p4w cereal"
	created, err := catalog.CreateTypedProduct(ctx, other, product.Product{ID: "typed-undo", Name: typed, UnitOfMeasure: "unit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-undo', ?, ?, ?)`,
		other.HouseholdID(), created.ID, other.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked, household_id)
		VALUES ('move-undo', 'item-undo', ?, 'in', 1, ?, 'manual', 0, ?)`,
		created.ID, at, other.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	effect, err := applyUndo(ctx, other, tx, "move-undo", true)
	if err != nil {
		t.Fatal(err)
	}
	if effect.Name != typed {
		t.Fatalf("undo name %q", effect.Name)
	}
	if _, err := applyUndo(ctx, primary, tx, "move-undo", true); err == nil {
		t.Fatal("primary undid cedar's move")
	}
}
