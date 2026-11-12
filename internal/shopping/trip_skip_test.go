package shopping_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestActiveRemoveSkip(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "active-skip", "Active")
	_, ok, err := deps.shopping.ActiveRemoveSkip(ctx, scope, itemID)
	if err != nil || ok {
		t.Fatalf("before remove: ok=%v err=%v", ok, err)
	}
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	_, ok, err = deps.shopping.ActiveRemoveSkip(ctx, scope, itemID)
	if err != nil || !ok {
		t.Fatalf("after remove: ok=%v err=%v", ok, err)
	}
}

func TestActiveRemoveSkipRequiresHousehold(t *testing.T) {
	deps := newTestStore(t)
	_, ok, err := deps.shopping.ActiveRemoveSkip(context.Background(), tenant.Scope{}, "item-x")
	if err == nil || ok {
		t.Fatalf("expected missing household: ok=%v err=%v", ok, err)
	}
	if !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("err: %v", err)
	}
}

func TestActiveRemoveSkipEmptyItemID(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	_, ok, err := deps.shopping.ActiveRemoveSkip(context.Background(), scope, "")
	if err != nil || ok {
		t.Fatalf("empty item: ok=%v err=%v", ok, err)
	}
}

func TestActiveRemoveSkipReturnsGroupID(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	household := scope.HouseholdID()
	const groupID = "grp-active-skip"
	if _, err := deps.db.Exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'Grp', 'grp', 'favorite', 1)`,
		groupID, household, household); err != nil {
		t.Fatal(err)
	}
	itemID := createTestItem(t, deps, ctx, "grp-skip-prod", "Grouped")
	if _, err := deps.db.Exec(`INSERT INTO product_group_members (household_id, group_id, product_id) SELECT household_id, ?, product_id FROM items WHERE id = ?`,
		groupID, itemID); err != nil {
		t.Fatal(err)
	}
	removedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := deps.db.Exec(`
		INSERT INTO staged_cart_skips (household_id, user_id, item_id, group_id, expires_trip, removed_at)
		SELECT ?, ?, ?, ?, trip_seq + 1, ? FROM households WHERE id = ?`,
		household, household, itemID, groupID, removedAt, household); err != nil {
		t.Fatal(err)
	}
	gotGroup, ok, err := deps.shopping.ActiveRemoveSkip(ctx, scope, itemID)
	if err != nil || !ok || gotGroup != groupID {
		t.Fatalf("group skip: group=%q ok=%v err=%v", gotGroup, ok, err)
	}
}

func TestActiveRemoveSkipNotFoundAfterTripExpires(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "expired-skip", "Expired")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	if err := deps.shopping.EndTrip(ctx, scope); err != nil {
		t.Fatal(err)
	}
	_, ok, err := deps.shopping.ActiveRemoveSkip(ctx, scope, itemID)
	if err != nil || ok {
		t.Fatalf("expired skip: ok=%v err=%v", ok, err)
	}
}
