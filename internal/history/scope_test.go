package history

import (
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestHistoryRejectsMissingHousehold(t *testing.T) {
	db := openHistoryDB(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	var scope tenant.Scope
	journal := NewJournal(db)

	if _, err := journal.Product(ctx, scope, "item", now); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("product: %v", err)
	}
	if _, err := journal.Group(ctx, scope, "group", now); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("group: %v", err)
	}
	if _, err := journal.Household(ctx, scope, LedgerFilter{Direction: "sideways"}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, scope, "move", 0, now); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("quantity: %v", err)
	}
	if _, err := journal.Undo(ctx, scope, "move", now); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("undo: %v", err)
	}
	if _, err := journal.PreviewUndo(ctx, scope, "move"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("preview: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := AttachStockIn(ctx, scope, tx, InNote{}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("stock in: %v", err)
	}
	if err := AttachStockOut(ctx, scope, tx, OutNote{}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("stock out: %v", err)
	}
}

func TestHouseholdCannotReadOrChangeAnotherHouseholdsMoves(t *testing.T) {
	db := openHistoryDB(t)
	ctx := t.Context()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	primary := scopeOf(t, db)
	const cedarID = "0190bbbb-cccc-7000-8000-0000000000e5"
	execHistory(t, db, `
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-01-01T00:00:00Z', '0123456789abcdef0123456789abcdef', 0)`, cedarID)
	cedar, err := tenant.New(cedarID)
	if err != nil {
		t.Fatal(err)
	}

	execHistory(t, db, `
		INSERT INTO products (id, name, category, unit_of_measure) VALUES
			('prod-cedar', 'Cedar Oats', 'Pantry', 'unit'),
			('prod-primary', 'Primary Oats', 'Pantry', 'unit')`)
	execHistory(t, db, `
		INSERT INTO items (id, user_id, product_id, household_id) VALUES
			('item-cedar', ?, 'prod-cedar', ?),
			('item-primary', ?, 'prod-primary', ?)`,
		cedar.HouseholdID(), cedar.HouseholdID(), primary.HouseholdID(), primary.HouseholdID())
	execHistory(t, db, `
		INSERT INTO product_groups (id, user_id, name, name_key, rule, household_id) VALUES
			('grp-cedar', ?, 'Cedar Group', 'cedar-group', 'same_as_ran_out', ?)`,
		cedar.HouseholdID(), cedar.HouseholdID())
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked, household_id) VALUES
			('move-cedar', 'item-cedar', 'prod-cedar', 'in', 3, ?, 'manual', 0, ?),
			('move-primary', 'item-primary', 'prod-primary', 'in', 2, ?, 'manual', 0, ?)`,
		at, cedar.HouseholdID(), at, primary.HouseholdID())

	journal := NewJournal(db)
	if _, err := journal.Product(ctx, primary, "item-cedar", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("primary read cedar item: %v", err)
	}
	if _, err := journal.Product(ctx, cedar, "item-primary", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("cedar read primary item: %v", err)
	}
	if _, err := journal.Group(ctx, primary, "grp-cedar", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("primary read cedar group: %v", err)
	}

	primaryLedger, err := journal.Household(ctx, primary, LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := ledgerIDs(primaryLedger.Moves); ids["move-cedar"] || !ids["move-primary"] {
		t.Fatalf("primary ledger = %+v", primaryLedger.Moves)
	}
	cedarLedger, err := journal.Household(ctx, cedar, LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := ledgerIDs(cedarLedger.Moves); ids["move-primary"] || !ids["move-cedar"] {
		t.Fatalf("cedar ledger = %+v", cedarLedger.Moves)
	}

	if _, err := journal.SetQuantity(ctx, primary, "move-cedar", 4, now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("primary edit: %v", err)
	}
	if _, err := journal.Undo(ctx, primary, "move-cedar", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("primary undo: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, cedar, "move-primary", 4, now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("cedar edit: %v", err)
	}
	if _, err := journal.Undo(ctx, cedar, "move-primary", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("cedar undo: %v", err)
	}

	var cedarQty, primaryQty int
	if err := db.QueryRowContext(ctx, `SELECT quantity FROM stock_moves WHERE id = ? AND household_id = ?`, "move-cedar", cedar.HouseholdID()).Scan(&cedarQty); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT quantity FROM stock_moves WHERE id = ? AND household_id = ?`, "move-primary", primary.HouseholdID()).Scan(&primaryQty); err != nil {
		t.Fatal(err)
	}
	if cedarQty != 3 || primaryQty != 2 {
		t.Fatalf("quantities cedar=%d primary=%d", cedarQty, primaryQty)
	}

	cedarView, err := journal.Product(ctx, cedar, "item-cedar", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cedarView.Moves) != 1 || cedarView.Moves[0].ID != "move-cedar" || cedarView.Moves[0].Quantity != 3 {
		t.Fatalf("cedar history = %+v", cedarView.Moves)
	}
	primaryView, err := journal.Product(ctx, primary, "item-primary", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(primaryView.Moves) != 1 || primaryView.Moves[0].ID != "move-primary" || primaryView.Moves[0].Quantity != 2 {
		t.Fatalf("primary history = %+v", primaryView.Moves)
	}
}

func ledgerIDs(moves []LedgerMove) map[string]bool {
	ids := map[string]bool{}
	for _, move := range moves {
		ids[move.ID] = true
	}
	return ids
}
