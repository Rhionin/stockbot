package cart

import (
	"context"
	"testing"
)

func TestProvisionFilterContext(t *testing.T) {
	ctx := context.Background()
	ctx = WithProvisionFilter(ctx, &ProvisionFilter{
		CanSendToCart: func(string, string, *string) bool { return true },
		ScopedItemIDs: map[string]struct{}{"item-a": {}},
	})
	filter, ok := ProvisionFilterFromContext(ctx)
	if !ok || filter == nil {
		t.Fatal("expected filter on context")
	}
	if filter.ScopedItemIDs == nil || len(filter.ScopedItemIDs) != 1 {
		t.Fatalf("scope: %#v", filter.ScopedItemIDs)
	}
	if _, ok := ProvisionFilterFromContext(context.Background()); ok {
		t.Fatal("empty context should have no filter")
	}
	if got := WithProvisionFilter(ctx, nil); got != ctx {
		t.Fatal("nil filter should return context unchanged")
	}
}
