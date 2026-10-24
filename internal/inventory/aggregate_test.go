package inventory_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/google/uuid"
)

func TestGetInventoryList_EmptyInventory(t *testing.T) {
	pantry, _, db := newTestPantry(t)
	items, err := pantry.GetInventoryList(context.Background(), scopeOf(t, db), time.Now(), 7, "")
	if err != nil {
		t.Fatalf("GetInventoryList failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

// TestGetInventoryList_SingleInstanceExpiryStates covers the single-item / single-instance
// cases for each expiry state plus the no-instances baseline.
func TestGetInventoryList_SingleInstanceExpiryStates(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	exp := func(d time.Duration) *time.Time { ts := now.Add(d); return &ts }

	tests := []struct {
		name           string
		expiresAt      *time.Time
		wantInstances  int
		wantNearExpiry int
		wantExpired    int
		wantNeedsAttn  bool
	}{
		{
			name:          "no instances",
			expiresAt:     nil,
			wantInstances: 0,
		},
		{
			name:          "ok instance (30 days out)",
			expiresAt:     exp(30 * 24 * time.Hour),
			wantInstances: 1,
		},
		{
			name:           "near-expiry instance (3 days out)",
			expiresAt:      exp(3 * 24 * time.Hour),
			wantInstances:  1,
			wantNearExpiry: 1,
			wantNeedsAttn:  true,
		},
		{
			name:          "expired instance (1 day ago)",
			expiresAt:     exp(-24 * time.Hour),
			wantInstances: 1,
			wantExpired:   1,
			wantNeedsAttn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)

			prodID := uuid.NewString()
			if err := catalog.CreateProduct(ctx, product.Product{
				ID: prodID, Name: "TestProduct", Category: "TestCat", UnitOfMeasure: "unit",
			}); err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}
			item, err := pantry.GetOrCreateItem(ctx, scope, prodID)
			if err != nil {
				t.Fatalf("GetOrCreateItem: %v", err)
			}

			if tt.expiresAt != nil || tt.wantInstances > 0 {
				// Only add an instance when the test wants one
				if tt.wantInstances > 0 {
					_, err = pantry.AddInstance(ctx, scope, inventory.ItemInstance{
						ItemID:    item.ID,
						StockInAt: now.Add(-24 * time.Hour),
						ExpiresAt: tt.expiresAt,
					})
					if err != nil {
						t.Fatalf("AddInstance: %v", err)
					}
				}
			}

			items, err := pantry.GetInventoryList(ctx, scope, now, 7, "")
			if err != nil {
				t.Fatalf("GetInventoryList: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}
			got := items[0]
			if got.InstanceCount != tt.wantInstances {
				t.Errorf("InstanceCount: got %d, want %d", got.InstanceCount, tt.wantInstances)
			}
			if got.NearExpiryCount != tt.wantNearExpiry {
				t.Errorf("NearExpiryCount: got %d, want %d", got.NearExpiryCount, tt.wantNearExpiry)
			}
			if got.ExpiredCount != tt.wantExpired {
				t.Errorf("ExpiredCount: got %d, want %d", got.ExpiredCount, tt.wantExpired)
			}
			if got.NeedsAttention != tt.wantNeedsAttn {
				t.Errorf("NeedsAttention: got %v, want %v", got.NeedsAttention, tt.wantNeedsAttn)
			}
		})
	}
}

func TestGetInventoryList_MixedExpiryStates(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	now := time.Now()

	prodID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: prodID, Name: "Eggs", Category: "Dairy", UnitOfMeasure: "dozen",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, prodID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	for _, d := range []time.Duration{
		30 * 24 * time.Hour, // ok
		3 * 24 * time.Hour,  // near expiry
		-24 * time.Hour,     // expired
	} {
		ts := now.Add(d)
		if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
			ItemID: item.ID, StockInAt: now, ExpiresAt: &ts,
		}); err != nil {
			t.Fatalf("AddInstance: %v", err)
		}
	}
	// one instance with no expiry
	if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
		ItemID: item.ID, StockInAt: now,
	}); err != nil {
		t.Fatalf("AddInstance (no expiry): %v", err)
	}

	items, err := pantry.GetInventoryList(ctx, scope, now, 7, "")
	if err != nil {
		t.Fatalf("GetInventoryList: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	got := items[0]
	if got.InstanceCount != 4 {
		t.Errorf("InstanceCount: got %d, want 4", got.InstanceCount)
	}
	if got.NearExpiryCount != 1 {
		t.Errorf("NearExpiryCount: got %d, want 1", got.NearExpiryCount)
	}
	if got.ExpiredCount != 1 {
		t.Errorf("ExpiredCount: got %d, want 1", got.ExpiredCount)
	}
	if !got.NeedsAttention {
		t.Error("NeedsAttention: got false, want true")
	}
}

func TestGetInventoryList_MultipleItems(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	now := time.Now()

	for _, p := range []struct {
		name, cat string
		offset    time.Duration
	}{
		{"Milk", "Dairy", -24 * time.Hour},       // expired
		{"Bread", "Bakery", 30 * 24 * time.Hour}, // ok
	} {
		prodID := uuid.NewString()
		if err := catalog.CreateProduct(ctx, product.Product{
			ID: prodID, Name: p.name, Category: p.cat, UnitOfMeasure: "unit",
		}); err != nil {
			t.Fatalf("CreateProduct %s: %v", p.name, err)
		}
		item, err := pantry.GetOrCreateItem(ctx, scope, prodID)
		if err != nil {
			t.Fatalf("GetOrCreateItem %s: %v", p.name, err)
		}
		ts := now.Add(p.offset)
		if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
			ItemID: item.ID, StockInAt: now, ExpiresAt: &ts,
		}); err != nil {
			t.Fatalf("AddInstance %s: %v", p.name, err)
		}
	}

	items, err := pantry.GetInventoryList(ctx, scope, now, 7, "")
	if err != nil {
		t.Fatalf("GetInventoryList: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	byName := make(map[string]inventory.InventoryItem, len(items))
	for _, it := range items {
		byName[it.Item.Product.Name] = it
	}

	milk := byName["Milk"]
	if !milk.NeedsAttention || milk.ExpiredCount != 1 {
		t.Errorf("Milk: NeedsAttention=%v ExpiredCount=%d, want true/1", milk.NeedsAttention, milk.ExpiredCount)
	}
	bread := byName["Bread"]
	if bread.NeedsAttention || bread.ExpiredCount != 0 {
		t.Errorf("Bread: NeedsAttention=%v ExpiredCount=%d, want false/0", bread.NeedsAttention, bread.ExpiredCount)
	}
}

func TestGetInventoryList_ExcludesRemovedInstances(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	now := time.Now()

	prodID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: prodID, Name: "Butter", Category: "Dairy", UnitOfMeasure: "stick",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, prodID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	farFuture := now.Add(30 * 24 * time.Hour)
	if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
		ItemID: item.ID, StockInAt: now, ExpiresAt: &farFuture,
	}); err != nil {
		t.Fatalf("AddInstance (active): %v", err)
	}
	inst2, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
		ItemID: item.ID, StockInAt: now, ExpiresAt: &farFuture,
	})
	if err != nil {
		t.Fatalf("AddInstance (to remove): %v", err)
	}
	if err := pantry.RemoveInstance(ctx, scope, inst2.ID, "consumed"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	items, err := pantry.GetInventoryList(ctx, scope, now, 7, "")
	if err != nil {
		t.Fatalf("GetInventoryList: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].InstanceCount != 1 {
		t.Errorf("InstanceCount: got %d, want 1 (removed instance excluded)", items[0].InstanceCount)
	}
}

func TestGetInventoryList_WithSearchFilter(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	now := time.Now()

	for _, p := range []struct{ name, category string }{
		{"Milk", "Dairy"}, {"Bread", "Bakery"}, {"Cheese", "Dairy"}, {"Rice", "Grain"},
	} {
		prodID := uuid.NewString()
		if err := catalog.CreateProduct(ctx, product.Product{
			ID: prodID, Name: p.name, Category: p.category, UnitOfMeasure: "unit",
		}); err != nil {
			t.Fatalf("CreateProduct %s: %v", p.name, err)
		}
		if _, err := pantry.GetOrCreateItem(ctx, scope, prodID); err != nil {
			t.Fatalf("GetOrCreateItem %s: %v", p.name, err)
		}
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"Milk", "Bread", "Cheese", "Rice"}},
		{"milk", []string{"Milk"}},
		{"BREAD", []string{"Bread"}},
		{"dairy", []string{"Milk", "Cheese"}},
		{"BAKERY", []string{"Bread"}},
		{"ai", []string{"Milk", "Cheese", "Rice"}}, // "Dairy" and "Grain" both contain "ai"
		{"xyz", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			items, err := pantry.GetInventoryList(ctx, scope, now, 7, tt.query)
			if err != nil {
				t.Fatalf("GetInventoryList: %v", err)
			}
			if len(items) != len(tt.want) {
				t.Fatalf("query %q: got %d items, want %d", tt.query, len(items), len(tt.want))
			}
			got := make(map[string]bool, len(items))
			for _, item := range items {
				got[item.Item.Product.Name] = true
			}
			for _, name := range tt.want {
				if !got[name] {
					t.Errorf("query %q: missing %q", tt.query, name)
				}
			}
		})
	}
}

// TestGetInventoryItem_MatchesGetInventoryList locks in that GetInventoryItem
// and GetInventoryList agree on the aggregated counts for the same item, since
// both share the aggregateItem implementation.
func TestGetInventoryItem_MatchesGetInventoryList(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	now := time.Now()

	prodID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: prodID, Name: "Eggs", Category: "Dairy", UnitOfMeasure: "dozen",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, prodID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	for _, d := range []time.Duration{
		30 * 24 * time.Hour, // ok
		3 * 24 * time.Hour,  // near expiry
		-24 * time.Hour,     // expired
	} {
		ts := now.Add(d)
		if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
			ItemID: item.ID, StockInAt: now, ExpiresAt: &ts,
		}); err != nil {
			t.Fatalf("AddInstance: %v", err)
		}
	}

	listItems, err := pantry.GetInventoryList(ctx, scope, now, 7, "")
	if err != nil {
		t.Fatalf("GetInventoryList: %v", err)
	}
	if len(listItems) != 1 {
		t.Fatalf("expected 1 item, got %d", len(listItems))
	}
	want := listItems[0]

	got, err := pantry.GetInventoryItem(ctx, scope, item.ID, now, 7)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}
	if got == nil {
		t.Fatal("expected InventoryItem, got nil")
	}
	if got.InstanceCount != want.InstanceCount {
		t.Errorf("InstanceCount: got %d, want %d", got.InstanceCount, want.InstanceCount)
	}
	if got.NearExpiryCount != want.NearExpiryCount {
		t.Errorf("NearExpiryCount: got %d, want %d", got.NearExpiryCount, want.NearExpiryCount)
	}
	if got.ExpiredCount != want.ExpiredCount {
		t.Errorf("ExpiredCount: got %d, want %d", got.ExpiredCount, want.ExpiredCount)
	}
	if got.NeedsAttention != want.NeedsAttention {
		t.Errorf("NeedsAttention: got %v, want %v", got.NeedsAttention, want.NeedsAttention)
	}
}

// TestGetInventoryItem_UnknownItemID locks in that an unrecognized itemID
// returns a nil InventoryItem and no error, rather than an error or a
// zero-valued InventoryItem.
func TestGetInventoryItem_UnknownItemID(t *testing.T) {
	pantry, _, db := newTestPantry(t)
	ctx := context.Background()

	got, err := pantry.GetInventoryItem(ctx, scopeOf(t, db), uuid.NewString(), time.Now(), 7)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}
