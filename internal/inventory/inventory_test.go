package inventory_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func scopeOf(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

const cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

func cedarScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`,
		cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// newTestPantry opens an in-memory SQLite database, applies all migrations,
// and returns an inventory Pantry.
func newTestPantry(t *testing.T) (*inventory.Pantry, *product.Catalog, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return inventory.NewPantry(conn), product.NewCatalog(conn), conn
}

// TestGetOrCreateItem_ProductImageURL locks in that Item.Product.ImageURL is
// projected through the items-products join, not silently dropped like the
// other product columns would be if a join query omitted it.
func TestGetOrCreateItem_ProductImageURL(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)

	const wantImageURL = "https://images.openfoodfacts.org/thumb.jpg"
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: "prod-1", Name: "Milk", Category: "Dairy", ImageURL: wantImageURL,
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	item, err := pantry.GetOrCreateItem(ctx, scope, "prod-1")
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	if item.Product == nil || item.Product.ImageURL != wantImageURL {
		t.Errorf("GetOrCreateItem: want Product.ImageURL %q, got %+v", wantImageURL, item.Product)
	}

	items, err := pantry.ListItems(ctx, scope)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Product.ImageURL != wantImageURL {
		t.Fatalf("ListItems: want 1 item with Product.ImageURL %q, got %+v", wantImageURL, items)
	}
}

// createTestProduct is a helper that creates a product for testing.
func createTestProduct(t *testing.T, catalog *product.Catalog, ctx context.Context, id, name string) {
	t.Helper()
	p := product.Product{ID: id, Name: name, Category: "Test"}
	if err := catalog.CreateProduct(ctx, p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
}

// --------------------------------------------------------------------------
// TestGetOrCreateItem
// --------------------------------------------------------------------------

func TestGetOrCreateItem(t *testing.T) {
	tests := []struct {
		name      string
		productID string
		setup     func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context)
		wantNew   bool // true if we expect a new item to be created
	}{
		{
			name:      "creates new item when none exists",
			productID: "prod-1",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) {
				createTestProduct(t, catalog, ctx, "prod-1", "Milk")
			},
			wantNew: true,
		},
		{
			name:      "returns existing item",
			productID: "prod-1",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) {
				createTestProduct(t, catalog, ctx, "prod-1", "Milk")
				_, err := pantry.GetOrCreateItem(ctx, scope, "prod-1")
				if err != nil {
					t.Fatalf("GetOrCreateItem setup: %v", err)
				}
			},
			wantNew: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			tt.setup(t, pantry, catalog, scope, ctx)

			item1, err := pantry.GetOrCreateItem(ctx, scope, tt.productID)
			if err != nil {
				t.Fatalf("GetOrCreateItem (first): %v", err)
			}
			if item1 == nil {
				t.Fatal("expected item, got nil")
			}
			if item1.UserID != scope.HouseholdID() {
				t.Errorf("UserID: want %q, got %q", scope.HouseholdID(), item1.UserID)
			}
			if item1.ProductID != tt.productID {
				t.Errorf("ProductID: want %q, got %q", tt.productID, item1.ProductID)
			}

			item2, err := pantry.GetOrCreateItem(ctx, scope, tt.productID)
			if err != nil {
				t.Fatalf("GetOrCreateItem (second): %v", err)
			}
			if item2 == nil {
				t.Fatal("expected item, got nil")
			}
			if item1.ID != item2.ID {
				t.Errorf("expected same item ID, got %q then %q", item1.ID, item2.ID)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestListItems
// --------------------------------------------------------------------------

func TestListItems(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, db *sql.DB, scope tenant.Scope, ctx context.Context)
		expectCount int
		expectOrder []string // product names in expected order
	}{
		{
			name: "empty inventory",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, db *sql.DB, scope tenant.Scope, ctx context.Context) {
			},
			expectCount: 0,
			expectOrder: []string{},
		},
		{
			name: "single item",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, db *sql.DB, scope tenant.Scope, ctx context.Context) {
				createTestProduct(t, catalog, ctx, "p1", "Apple Juice")
				_, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}
			},
			expectCount: 1,
			expectOrder: []string{"Apple Juice"},
		},
		{
			name: "multiple items ordered by product name",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, db *sql.DB, scope tenant.Scope, ctx context.Context) {
				createTestProduct(t, catalog, ctx, "p1", "Cheese")
				createTestProduct(t, catalog, ctx, "p2", "Apple Juice")
				createTestProduct(t, catalog, ctx, "p3", "Butter")

				for _, pid := range []string{"p1", "p2", "p3"} {
					_, err := pantry.GetOrCreateItem(ctx, scope, pid)
					if err != nil {
						t.Fatalf("GetOrCreateItem: %v", err)
					}
				}
			},
			expectCount: 3,
			expectOrder: []string{"Apple Juice", "Butter", "Cheese"},
		},
		{
			name: "filters by household",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, db *sql.DB, scope tenant.Scope, ctx context.Context) {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				createTestProduct(t, catalog, ctx, "p2", "Bread")

				_, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem primary: %v", err)
				}
				_, err = pantry.GetOrCreateItem(ctx, cedarScope(t, db), "p2")
				if err != nil {
					t.Fatalf("GetOrCreateItem cedar: %v", err)
				}
			},
			expectCount: 1,
			expectOrder: []string{"Milk"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			tt.setup(t, pantry, catalog, db, scope, ctx)

			items, err := pantry.ListItems(ctx, scope)
			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			if len(items) != tt.expectCount {
				t.Fatalf("expected %d items, got %d", tt.expectCount, len(items))
			}

			// Verify order by product name
			for i, wantName := range tt.expectOrder {
				if items[i].Product.Name != wantName {
					t.Errorf("[%d] Product.Name: want %q, got %q", i, wantName, items[i].Product.Name)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestListItemInstances
// --------------------------------------------------------------------------

func TestListItemInstances(t *testing.T) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)
	nextWeek := now.Add(7 * 24 * time.Hour)

	tests := []struct {
		name              string
		setup             func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string // returns itemID
		expectCount       int
		expectExpiryOrder []bool // true if expires_at is not nil, in expected order
	}{
		{
			name: "empty",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}
				return item.ID
			},
			expectCount:       0,
			expectExpiryOrder: []bool{},
		},
		{
			name: "single instance",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}

				_, err = pantry.AddInstance(ctx, scope, inventory.ItemInstance{
					ItemID:    item.ID,
					StockInAt: now,
					ExpiresAt: &tomorrow,
				})
				if err != nil {
					t.Fatalf("AddInstance: %v", err)
				}
				return item.ID
			},
			expectCount:       1,
			expectExpiryOrder: []bool{true},
		},
		{
			name: "multiple instances ordered by expiry (use-oldest-first)",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}

				// Add instances in random order
				instances := []inventory.ItemInstance{
					{ItemID: item.ID, StockInAt: now, ExpiresAt: &nextWeek},
					{ItemID: item.ID, StockInAt: now, ExpiresAt: &tomorrow},
					{ItemID: item.ID, StockInAt: now, ExpiresAt: nil}, // no expiry should come last
				}

				for _, inst := range instances {
					_, err = pantry.AddInstance(ctx, scope, inst)
					if err != nil {
						t.Fatalf("AddInstance: %v", err)
					}
				}
				return item.ID
			},
			expectCount:       3,
			expectExpiryOrder: []bool{true, true, false}, // sorted by expiry, nulls last
		},
		{
			name: "excludes removed instances",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}

				// Add two instances
				inst1, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
					ItemID:    item.ID,
					StockInAt: now,
				})
				if err != nil {
					t.Fatalf("AddInstance 1: %v", err)
				}

				_, err = pantry.AddInstance(ctx, scope, inventory.ItemInstance{
					ItemID:    item.ID,
					StockInAt: now,
				})
				if err != nil {
					t.Fatalf("AddInstance 2: %v", err)
				}

				// Remove the first instance
				err = pantry.RemoveInstance(ctx, scope, inst1.ID, "manual")
				if err != nil {
					t.Fatalf("RemoveInstance: %v", err)
				}

				return item.ID
			},
			expectCount:       1, // only one remaining
			expectExpiryOrder: []bool{false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			itemID := tt.setup(t, pantry, catalog, scope, ctx)

			instances, err := pantry.ListItemInstances(ctx, scope, itemID)
			if err != nil {
				t.Fatalf("ListItemInstances: %v", err)
			}
			if len(instances) != tt.expectCount {
				t.Fatalf("expected %d instances, got %d", tt.expectCount, len(instances))
			}

			// Verify expiry order
			for i, wantHasExpiry := range tt.expectExpiryOrder {
				hasExpiry := instances[i].ExpiresAt != nil
				if hasExpiry != wantHasExpiry {
					t.Errorf("[%d] ExpiresAt != nil: want %v, got %v", i, wantHasExpiry, hasExpiry)
				}
			}

			// Verify instances are sorted by expiry date ascending
			if len(instances) > 1 {
				for i := 0; i < len(instances)-1; i++ {
					curr := instances[i]
					next := instances[i+1]

					// If current has no expiry, all following should have no expiry
					if curr.ExpiresAt == nil && next.ExpiresAt != nil {
						t.Errorf("instances not sorted correctly: nil expiry should come after non-nil")
					}

					// If both have expiry, current should be <= next
					if curr.ExpiresAt != nil && next.ExpiresAt != nil {
						if curr.ExpiresAt.After(*next.ExpiresAt) {
							t.Errorf("instances not sorted correctly: %v > %v", curr.ExpiresAt, next.ExpiresAt)
						}
					}
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestAddInstance
// --------------------------------------------------------------------------

func TestAddInstance(t *testing.T) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)

	tests := []struct {
		name          string
		instance      inventory.ItemInstance
		wantID        bool // true if we expect ID to be generated
		wantExpiresAt bool // true if we expect ExpiresAt to be set
	}{
		{
			name: "with explicit ID and expiry",
			instance: inventory.ItemInstance{
				ID:        "inst-1",
				StockInAt: now,
				ExpiresAt: &tomorrow,
			},
			wantID:        false,
			wantExpiresAt: true,
		},
		{
			name: "generate UUID when ID empty",
			instance: inventory.ItemInstance{
				StockInAt: now,
			},
			wantID:        true,
			wantExpiresAt: false,
		},
		{
			name: "with no expiry date",
			instance: inventory.ItemInstance{
				StockInAt: now,
				ExpiresAt: nil,
			},
			wantID:        true,
			wantExpiresAt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)

			// Setup: create product and item
			createTestProduct(t, catalog, ctx, "p1", "Milk")
			item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
			if err != nil {
				t.Fatalf("GetOrCreateItem: %v", err)
			}

			tt.instance.ItemID = item.ID

			// Add instance
			added, err := pantry.AddInstance(ctx, scope, tt.instance)
			if err != nil {
				t.Fatalf("AddInstance: %v", err)
			}
			if added == nil {
				t.Fatal("expected instance, got nil")
			}

			// Verify ID
			if tt.wantID && added.ID == "" {
				t.Error("expected generated UUID, got empty string")
			}
			if !tt.wantID && added.ID != tt.instance.ID {
				t.Errorf("ID: want %q, got %q", tt.instance.ID, added.ID)
			}

			// Verify ItemID
			if added.ItemID != item.ID {
				t.Errorf("ItemID: want %q, got %q", item.ID, added.ItemID)
			}

			// Verify ExpiresAt
			if tt.wantExpiresAt && added.ExpiresAt == nil {
				t.Error("expected ExpiresAt to be set, got nil")
			}
			if !tt.wantExpiresAt && added.ExpiresAt != nil {
				t.Errorf("expected ExpiresAt to be nil, got %v", added.ExpiresAt)
			}

			// Roundtrip: verify GetInstance retrieves the same instance
			retrieved, err := pantry.GetInstance(ctx, scope, added.ID)
			if err != nil {
				t.Fatalf("GetInstance: %v", err)
			}
			if retrieved == nil {
				t.Fatal("expected instance, got nil")
			}
			if retrieved.ID != added.ID {
				t.Errorf("ID: want %q, got %q", added.ID, retrieved.ID)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestRemoveInstance
// --------------------------------------------------------------------------

func TestRemoveInstance(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		setup       func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string // returns instanceID
		reason      string
		expectError error
	}{
		{
			name: "successfully removes existing instance",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}
				inst, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
					ItemID:    item.ID,
					StockInAt: now,
				})
				if err != nil {
					t.Fatalf("AddInstance: %v", err)
				}
				return inst.ID
			},
			reason:      "manual",
			expectError: nil,
		},
		{
			name: "returns error for non-existent instance",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				return "non-existent-id"
			},
			reason:      "manual",
			expectError: inventory.ErrInstanceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			instanceID := tt.setup(t, pantry, catalog, scope, ctx)

			err := pantry.RemoveInstance(ctx, scope, instanceID, tt.reason)

			if tt.expectError != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.expectError)
				}
				if !errors.Is(err, tt.expectError) {
					t.Fatalf("expected error %v, got %v", tt.expectError, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("RemoveInstance: %v", err)
			}

			// Verify instance is marked as removed
			inst, err := pantry.GetInstance(ctx, scope, instanceID)
			if err != nil {
				t.Fatalf("GetInstance: %v", err)
			}
			if inst == nil {
				t.Fatal("expected instance, got nil")
			}
			if inst.RemovedAt == nil {
				t.Error("expected RemovedAt to be set, got nil")
			}
			if inst.RemovalReason == nil || *inst.RemovalReason != tt.reason {
				t.Errorf("RemovalReason: want %q, got %v", tt.reason, inst.RemovalReason)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestGetInstance
// --------------------------------------------------------------------------

func TestGetInstance(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		setup       func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string // returns instanceID
		expectFound bool
	}{
		{
			name: "found",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}
				inst, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{
					ItemID:    item.ID,
					StockInAt: now,
				})
				if err != nil {
					t.Fatalf("AddInstance: %v", err)
				}
				return inst.ID
			},
			expectFound: true,
		},
		{
			name: "not found",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				return "non-existent-id"
			},
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			instanceID := tt.setup(t, pantry, catalog, scope, ctx)

			inst, err := pantry.GetInstance(ctx, scope, instanceID)
			if err != nil {
				t.Fatalf("GetInstance: %v", err)
			}

			if tt.expectFound && inst == nil {
				t.Fatal("expected instance, got nil")
			}
			if !tt.expectFound && inst != nil {
				t.Errorf("expected nil, got %+v", inst)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestUpdateTargetQuantity
// --------------------------------------------------------------------------

func TestUpdateTargetQuantity(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string // returns itemID
		qty         int
		expectError error
	}{
		{
			name: "successfully updates target quantity",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				createTestProduct(t, catalog, ctx, "p1", "Milk")
				item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
				if err != nil {
					t.Fatalf("GetOrCreateItem: %v", err)
				}
				return item.ID
			},
			qty:         5,
			expectError: nil,
		},
		{
			name: "returns error for unknown item",
			setup: func(t *testing.T, pantry *inventory.Pantry, catalog *product.Catalog, scope tenant.Scope, ctx context.Context) string {
				return "non-existent-id"
			},
			qty:         5,
			expectError: inventory.ErrInstanceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pantry, catalog, db := newTestPantry(t)
			ctx := context.Background()
			scope := scopeOf(t, db)
			itemID := tt.setup(t, pantry, catalog, scope, ctx)

			err := pantry.UpdateTargetQuantity(ctx, scope, itemID, tt.qty)

			if tt.expectError != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.expectError)
				}
				if !errors.Is(err, tt.expectError) {
					t.Fatalf("expected error %v, got %v", tt.expectError, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("UpdateTargetQuantity: %v", err)
			}

			item, err := pantry.GetItem(ctx, scope, itemID)
			if err != nil {
				t.Fatalf("GetItem: %v", err)
			}
			if item == nil {
				t.Fatal("expected item, got nil")
			}
			if item.TargetQuantity == nil || *item.TargetQuantity != tt.qty {
				t.Errorf("TargetQuantity: want %d, got %v", tt.qty, item.TargetQuantity)
			}
		})
	}
}

// --------------------------------------------------------------------------
// fakeBroadcaster and Pantry publish-behavior tests
// --------------------------------------------------------------------------

// fakeBroadcaster records every PublishInventoryEvent call it receives, so
// tests can assert on Pantry's publish behavior without depending on
// internal/events.
type fakeBroadcaster struct {
	published []inventory.InventoryItem
}

func (f *fakeBroadcaster) PublishInventoryEvent(item inventory.InventoryItem) {
	f.published = append(f.published, item)
}

// TestPantryBroadcaster_NilIsSafe locks in that leaving pantry.Broadcaster
// unset, as every other test in this file does, is a supported mode: none of
// the three mutating methods panics or changes its return value because of it.
func TestPantryBroadcaster_NilIsSafe(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	createTestProduct(t, catalog, ctx, "p1", "Milk")
	item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	inst, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{ItemID: item.ID, StockInAt: time.Now()})
	if err != nil {
		t.Fatalf("AddInstance: %v", err)
	}
	if inst == nil {
		t.Fatal("AddInstance: expected instance, got nil")
	}

	if err := pantry.UpdateTargetQuantity(ctx, scope, item.ID, 5); err != nil {
		t.Fatalf("UpdateTargetQuantity: %v", err)
	}

	if err := pantry.RemoveInstance(ctx, scope, inst.ID, "manual"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}
}

func TestAddInstance_PublishesInventoryEvent(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	createTestProduct(t, catalog, ctx, "p1", "Milk")
	item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	broadcaster := &fakeBroadcaster{}
	pantry.Broadcaster = broadcaster

	if _, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{ItemID: item.ID, StockInAt: time.Now()}); err != nil {
		t.Fatalf("AddInstance: %v", err)
	}

	want, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}
	if len(broadcaster.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(broadcaster.published))
	}
	if !reflect.DeepEqual(broadcaster.published[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.published[0], *want)
	}
}

func TestRemoveInstance_PublishesInventoryEvent(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	createTestProduct(t, catalog, ctx, "p1", "Milk")
	item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	inst, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{ItemID: item.ID, StockInAt: time.Now()})
	if err != nil {
		t.Fatalf("AddInstance: %v", err)
	}

	broadcaster := &fakeBroadcaster{}
	pantry.Broadcaster = broadcaster

	if err := pantry.RemoveInstance(ctx, scope, inst.ID, "manual"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	want, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}
	if len(broadcaster.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(broadcaster.published))
	}
	if !reflect.DeepEqual(broadcaster.published[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.published[0], *want)
	}
}

func TestRemoveInstance_UnknownInstance_NoPublish(t *testing.T) {
	pantry, _, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)

	broadcaster := &fakeBroadcaster{}
	pantry.Broadcaster = broadcaster

	err := pantry.RemoveInstance(ctx, scope, "non-existent-id", "manual")
	if !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound, got %v", err)
	}
	if len(broadcaster.published) != 0 {
		t.Errorf("expected 0 published events, got %d", len(broadcaster.published))
	}
}

func TestUpdateTargetQuantity_PublishesInventoryEvent(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	createTestProduct(t, catalog, ctx, "p1", "Milk")
	item, err := pantry.GetOrCreateItem(ctx, scope, "p1")
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}

	broadcaster := &fakeBroadcaster{}
	pantry.Broadcaster = broadcaster

	if err := pantry.UpdateTargetQuantity(ctx, scope, item.ID, 5); err != nil {
		t.Fatalf("UpdateTargetQuantity: %v", err)
	}

	want, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}
	if len(broadcaster.published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(broadcaster.published))
	}
	if !reflect.DeepEqual(broadcaster.published[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.published[0], *want)
	}
}

func TestUpdateTargetQuantity_UnknownItem_NoPublish(t *testing.T) {
	pantry, _, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)

	broadcaster := &fakeBroadcaster{}
	pantry.Broadcaster = broadcaster

	err := pantry.UpdateTargetQuantity(ctx, scope, "non-existent-id", 5)
	if !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound, got %v", err)
	}
	if len(broadcaster.published) != 0 {
		t.Errorf("expected 0 published events, got %d", len(broadcaster.published))
	}
}

func TestWipe_ClearsOneUsersStockAndLeavesProductLookup(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	now := time.Now()
	scope := scopeOf(t, db)
	other := cedarScope(t, db)

	if err := catalog.CreateProduct(ctx, product.Product{
		ID: "prod-1", Name: "Milk", Category: "Dairy",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, "111", "prod-1", "global", ""); err != nil {
		t.Fatalf("UpsertBarcodeMapping: %v", err)
	}
	if err := catalog.RecordBarcodeMiss(ctx, "unknown-barcode", now); err != nil {
		t.Fatalf("RecordBarcodeMiss: %v", err)
	}

	for _, row := range []struct {
		scope tenant.Scope
		label string
	}{
		{scope, "primary"},
		{other, "cedar"},
	} {
		item, err := pantry.GetOrCreateItem(ctx, row.scope, "prod-1")
		if err != nil {
			t.Fatalf("GetOrCreateItem %s: %v", row.label, err)
		}
		if _, err := pantry.AddInstance(ctx, row.scope, inventory.ItemInstance{
			ItemID: item.ID, StockInAt: now,
		}); err != nil {
			t.Fatalf("AddInstance %s: %v", row.label, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO consumption_events (id, item_id, consumed_at, household_id) VALUES (?, ?, ?, ?)`,
			"cons-"+row.label, item.ID, now, row.scope.HouseholdID()); err != nil {
			t.Fatalf("consumption event %s: %v", row.label, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id) VALUES (?, ?, ?, 1, 'manual', ?)`,
			"shop-"+row.label, row.scope.HouseholdID(), item.ID, row.scope.HouseholdID()); err != nil {
			t.Fatalf("shopping item %s: %v", row.label, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		 VALUES ('scan-1', ?, '111', ?, 'stock_in', 1, 'pending', 'prod-1', ?)`, scope.HouseholdID(), now, scope.HouseholdID()); err != nil {
		t.Fatalf("scan entry: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', ?), ('supply_months', '4')`,
		now.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO stock_in_events (product_id, at, household_id) VALUES ('prod-1', ?, ?)`, now, scope.HouseholdID()); err != nil {
		t.Fatalf("stock-in: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO supply_overrides (product_id, quantity, household_id) VALUES ('prod-1', 4, ?)`, scope.HouseholdID()); err != nil {
		t.Fatalf("override: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO brand_preferences (user_id, need_key, item_id, ignore_price, household_id) VALUES (?, 'milk', 'pref-item', 0, ?)`,
		scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatalf("preference: %v", err)
	}

	if err := supply.Open(db).Wipe(ctx, scope, supply.WipePhrase); err != nil {
		t.Fatalf("Wipe: %v", err)
	}

	wiped, err := pantry.ListItems(ctx, scope)
	if err != nil {
		t.Fatalf("ListItems primary: %v", err)
	}
	if len(wiped) != 0 {
		t.Fatalf("primary items after wipe: got %d, want 0", len(wiped))
	}

	kept, err := pantry.ListItems(ctx, other)
	if err != nil {
		t.Fatalf("ListItems cedar: %v", err)
	}
	if len(kept) != 1 {
		t.Fatalf("cedar items after wipe: got %d, want 1", len(kept))
	}
	instances, err := pantry.ListItemInstances(ctx, other, kept[0].ID)
	if err != nil {
		t.Fatalf("ListItemInstances cedar: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("cedar instances after wipe: got %d, want 1", len(instances))
	}

	products, err := catalog.ListProducts(ctx, scope)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(products) != 0 {
		t.Fatalf("shelved product still in the picker: %+v", products)
	}
	keptProduct, err := catalog.GetProductByID(ctx, "prod-1")
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if keptProduct == nil || keptProduct.Name != "Milk" {
		t.Fatalf("product after wipe: got %+v", keptProduct)
	}
	found, err := catalog.LookupByBarcode(ctx, scope, "111")
	if err != nil {
		t.Fatalf("LookupByBarcode: %v", err)
	}
	if found == nil || found.Name != "Milk" {
		t.Fatalf("barcode lookup after wipe: got %+v", found)
	}
	miss, err := catalog.GetBarcodeMiss(ctx, "unknown-barcode")
	if err != nil {
		t.Fatalf("GetBarcodeMiss: %v", err)
	}
	if miss == nil {
		t.Fatal("barcode miss was deleted")
	}

	assertCount(t, db, `SELECT COUNT(*) FROM consumption_events WHERE id = 'cons-primary'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM consumption_events WHERE id = 'cons-cedar'`, 1)
	assertCountArgs(t, db, `SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ?`, 0, scope.HouseholdID())
	assertCountArgs(t, db, `SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ?`, 1, other.HouseholdID())
	assertCount(t, db, `SELECT COUNT(*) FROM scan_entries`, 1)
	assertCountArgs(t, db, `SELECT COUNT(*) FROM item_instances WHERE household_id = ?`, 0, scope.HouseholdID())
	assertCount(t, db, `SELECT COUNT(*) FROM app_settings WHERE key = 'onboarding_started_at'`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM app_settings WHERE key = 'supply_months' AND value = '4'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM supply_overrides WHERE product_id = 'prod-1' AND quantity = 4`, 1)
	assertCountArgs(t, db, `SELECT COUNT(*) FROM brand_preferences WHERE household_id = ?`, 1, scope.HouseholdID())
	assertCount(t, db, `SELECT COUNT(*) FROM stock_in_events`, 0)
}

func TestGetOrCreateItemFindsExistingHouseholdRow(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	createTestProduct(t, catalog, ctx, "prod-kept", "Oats")

	var legacy string
	if err := db.QueryRow(`SELECT legacy_user_id FROM households WHERE is_primary = 1`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-kept', ?, 'prod-kept', ?)`,
		legacy, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}

	item, err := pantry.GetOrCreateItem(ctx, scope, "prod-kept")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "item-kept" {
		t.Fatalf("item id %s", item.ID)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM items WHERE product_id = 'prod-kept'`, 1)
}

func TestScopeRequired(t *testing.T) {
	pantry, _, db := newTestPantry(t)
	ctx := context.Background()
	var scope tenant.Scope
	now := time.Now()

	requireMissing := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	_, err := pantry.GetOrCreateItem(ctx, scope, "prod")
	requireMissing("GetOrCreateItem", err)
	_, err = pantry.ListItems(ctx, scope)
	requireMissing("ListItems", err)
	_, err = pantry.ListItemInstances(ctx, scope, "item")
	requireMissing("ListItemInstances", err)
	_, err = pantry.AddInstance(ctx, scope, inventory.ItemInstance{ItemID: "item", StockInAt: now})
	requireMissing("AddInstance", err)
	requireMissing("UnshelveItem", inventory.UnshelveItem(ctx, db, scope, "item"))
	requireMissing("RemoveInstance", pantry.RemoveInstance(ctx, scope, "inst", "manual"))
	requireMissing("UpdateTargetQuantity", pantry.UpdateTargetQuantity(ctx, scope, "item", 1))
	requireMissing("UpdateReplenishmentMode", pantry.UpdateReplenishmentMode(ctx, scope, "item", inventory.TargetMode))
	_, err = pantry.GetTargetQuantity(ctx, scope, "item")
	requireMissing("GetTargetQuantity", err)
	_, err = pantry.GetReplenishmentMode(ctx, scope, "item")
	requireMissing("GetReplenishmentMode", err)
	_, err = pantry.List(ctx, scope)
	requireMissing("List", err)
	_, err = pantry.GetInventoryList(ctx, scope, now, 7, "")
	requireMissing("GetInventoryList", err)
	_, err = pantry.GetInventoryItem(ctx, scope, "item", now, 7)
	requireMissing("GetInventoryItem", err)
	_, err = pantry.GetItem(ctx, scope, "item")
	requireMissing("GetItem", err)
	_, err = pantry.GetInstance(ctx, scope, "inst")
	requireMissing("GetInstance", err)
}

func TestCrossHouseholdCannotSeeOrChangeTheOtherPantry(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	now := time.Now()
	scopeA := scopeOf(t, db)
	scopeB := cedarScope(t, db)
	createTestProduct(t, catalog, ctx, "prod-shared", "Milk")

	itemA, err := pantry.GetOrCreateItem(ctx, scopeA, "prod-shared")
	if err != nil {
		t.Fatal(err)
	}
	itemB, err := pantry.GetOrCreateItem(ctx, scopeB, "prod-shared")
	if err != nil {
		t.Fatal(err)
	}
	if itemA.ID == itemB.ID {
		t.Fatal("households share an item id")
	}
	instA, err := pantry.AddInstance(ctx, scopeA, inventory.ItemInstance{ItemID: itemA.ID, StockInAt: now})
	if err != nil {
		t.Fatal(err)
	}
	instB, err := pantry.AddInstance(ctx, scopeB, inventory.ItemInstance{ItemID: itemB.ID, StockInAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := pantry.UpdateReplenishmentMode(ctx, scopeB, itemB.ID, inventory.ReplenishMode); err != nil {
		t.Fatal(err)
	}

	itemsA, err := pantry.ListItems(ctx, scopeA)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsA) != 1 || itemsA[0].ID != itemA.ID {
		t.Fatalf("list A: %+v", itemsA)
	}
	itemsB, err := pantry.ListItems(ctx, scopeB)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsB) != 1 || itemsB[0].ID != itemB.ID {
		t.Fatalf("list B: %+v", itemsB)
	}
	listed, err := pantry.List(ctx, scopeA)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != instA.ID {
		t.Fatalf("instances A: %+v", listed)
	}
	listedB, err := pantry.List(ctx, scopeB)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedB) != 1 || listedB[0].ID != instB.ID {
		t.Fatalf("instances B: %+v", listedB)
	}
	inv, err := pantry.GetInventoryList(ctx, scopeA, now, 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 1 || inv[0].Item.ID != itemA.ID {
		t.Fatalf("inventory A: %+v", inv)
	}
	foreignInstances, err := pantry.ListItemInstances(ctx, scopeA, itemB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(foreignInstances) != 0 {
		t.Fatalf("A listed B's instances: %+v", foreignInstances)
	}

	if got, err := pantry.GetItem(ctx, scopeA, itemB.ID); err != nil || got != nil {
		t.Fatalf("GetItem foreign: %+v %v", got, err)
	}
	if got, err := pantry.GetInstance(ctx, scopeA, instB.ID); err != nil || got != nil {
		t.Fatalf("GetInstance foreign: %+v %v", got, err)
	}
	if got, err := pantry.GetInventoryItem(ctx, scopeA, itemB.ID, now, 7); err != nil || got != nil {
		t.Fatalf("GetInventoryItem foreign: %+v %v", got, err)
	}
	if _, err := pantry.GetTargetQuantity(ctx, scopeA, itemB.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetTargetQuantity foreign: %v", err)
	}
	if _, err := pantry.GetReplenishmentMode(ctx, scopeA, itemB.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetReplenishmentMode foreign: %v", err)
	}

	if err := pantry.UpdateTargetQuantity(ctx, scopeA, itemB.ID, 9); !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("UpdateTargetQuantity foreign: %v", err)
	}
	if err := pantry.UpdateReplenishmentMode(ctx, scopeA, itemB.ID, inventory.TargetMode); !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("UpdateReplenishmentMode foreign: %v", err)
	}
	mode, err := pantry.GetReplenishmentMode(ctx, scopeB, itemB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mode != inventory.ReplenishMode {
		t.Fatalf("B mode changed to %s", mode)
	}
	if _, err := pantry.AddInstance(ctx, scopeA, inventory.ItemInstance{ItemID: itemB.ID, StockInAt: now}); !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("AddInstance foreign: %v", err)
	}
	if _, err := db.Exec(`UPDATE items SET shelved = 1 WHERE id = ? AND household_id = ?`, itemB.ID, scopeB.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if err := inventory.UnshelveItem(ctx, db, scopeA, itemB.ID); err != nil {
		t.Fatal(err)
	}
	var shelved int
	if err := db.QueryRow(`SELECT shelved FROM items WHERE id = ?`, itemB.ID).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 1 {
		t.Fatalf("A unshelved B's item, shelved=%d", shelved)
	}
	if err := pantry.RemoveInstance(ctx, scopeA, instB.ID, "manual"); !errors.Is(err, inventory.ErrInstanceNotFound) {
		t.Fatalf("RemoveInstance foreign: %v", err)
	}
	kept, err := pantry.GetInstance(ctx, scopeB, instB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept == nil || kept.RemovedAt != nil {
		t.Fatalf("B's instance changed: %+v", kept)
	}
	again, err := pantry.GetOrCreateItem(ctx, scopeA, "prod-shared")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != itemA.ID {
		t.Fatalf("A's lookup returned %s", again.ID)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	assertCountArgs(t, db, query, want)
}

func assertCountArgs(t *testing.T, db *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	if got != want {
		t.Fatalf("count %s: got %d, want %d", query, got, want)
	}
}
