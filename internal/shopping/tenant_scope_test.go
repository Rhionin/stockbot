package shopping_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"
	cedarChannelKey  = "0123456789abcdef0123456789abcdef"
	cedarCreatedAt   = "2026-01-15T12:00:00Z"
)

func insertCedarHousehold(t *testing.T, deps testDeps) {
	t.Helper()
	_, err := deps.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
		VALUES (?, 'Household Cedar', '', ?, ?, 0, '')`,
		cedarHouseholdID, cedarCreatedAt, cedarChannelKey)
	if err != nil {
		t.Fatal(err)
	}
}

func TestShoppingRejectsZeroScope(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	var zero tenant.Scope
	if _, err := deps.shopping.ListManualItems(ctx, zero); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("list: %v", err)
	}
	if _, err := deps.shopping.AddManualItem(ctx, zero, "item", 1); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("add: %v", err)
	}
	if err := deps.shopping.MarkPurchased(ctx, zero, "line"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("purchase: %v", err)
	}
	if err := deps.shopping.RemoveItem(ctx, zero, "line"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("remove: %v", err)
	}
}

func TestShoppingListHidesAnotherHousehold(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	insertCedarHousehold(t, deps)
	if _, err := deps.db.Exec(`INSERT INTO products (id, name) VALUES ('prod-cedar-oats', 'Cedar Oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.db.Exec(
		`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-cedar-oats', ?, 'prod-cedar-oats', ?)`,
		cedarHouseholdID, cedarHouseholdID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.db.Exec(
		`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source) VALUES ('line-cedar', ?, ?, 'item-cedar-oats', 3, 'manual')`,
		cedarHouseholdID, cedarHouseholdID,
	); err != nil {
		t.Fatal(err)
	}

	primary := scopeOf(t, deps.db)
	lines, err := deps.shopping.ListManualItems(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("household A listed Cedar's line: %+v", lines)
	}
	got, err := deps.shopping.GetItemByID(ctx, primary, "line-cedar")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("household A read Cedar's line: %+v", got)
	}
	if err := deps.shopping.MarkPurchased(ctx, primary, "line-cedar"); !errors.Is(err, shopping.ErrItemNotFound) {
		t.Fatalf("purchase: %v", err)
	}
	if err := deps.shopping.RemoveItem(ctx, primary, "line-cedar"); !errors.Is(err, shopping.ErrItemNotFound) {
		t.Fatalf("remove: %v", err)
	}

	var quantity int
	var open int
	if err := deps.db.QueryRow(
		`SELECT quantity, purchased_at IS NULL FROM shopping_list_items WHERE id = ? AND household_id = ?`,
		"line-cedar", cedarHouseholdID,
	).Scan(&quantity, &open); err != nil {
		t.Fatal(err)
	}
	if quantity != 3 || open != 1 {
		t.Fatalf("Cedar's line changed: quantity %d open %d", quantity, open)
	}

	cedar, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := deps.shopping.GetItemByID(ctx, cedar, "line-cedar")
	if err != nil {
		t.Fatal(err)
	}
	if owned == nil || owned.Quantity != 3 || owned.PurchasedAt != nil {
		t.Fatalf("Cedar could not read its line: %+v", owned)
	}
}
