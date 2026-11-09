package shopping_test

import (
	"context"
	"testing"
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
