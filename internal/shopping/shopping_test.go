package shopping_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
)

// testDeps holds all stores needed for shopping list tests.
type testDeps struct {
	db        *sql.DB
	shopping  *shopping.Store
	inventory *inventory.Pantry
	product   *product.Catalog
}

// newTestStore opens an in-memory SQLite database, applies all migrations,
// and returns stores for shopping, inventory, and product.
func newTestStore(t *testing.T) testDeps {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return testDeps{
		db:        conn,
		shopping:  shopping.NewStore(conn),
		inventory: inventory.NewPantry(conn),
		product:   product.NewCatalog(conn),
	}
}

// createTestItem creates a product and an inventory item, returning the item ID.
func createTestItem(t *testing.T, deps testDeps, ctx context.Context, userID, productID, productName string) string {
	t.Helper()
	p := product.Product{ID: productID, Name: productName, Category: "Test"}
	if err := deps.product.CreateProduct(ctx, p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	item, err := deps.inventory.GetOrCreateItem(ctx, userID, productID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	return item.ID
}

func TestAddManualItem(t *testing.T) {
	tests := []struct {
		name     string
		userID   string
		quantity int
	}{
		{name: "adds item with correct fields", userID: "user-1", quantity: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestStore(t)
			ctx := context.Background()

			itemID := createTestItem(t, deps, ctx, tt.userID, "prod-1", "Milk")

			got, err := deps.shopping.AddManualItem(ctx, tt.userID, itemID, tt.quantity)
			if err != nil {
				t.Fatalf("AddManualItem: %v", err)
			}
			if got == nil {
				t.Fatal("expected item, got nil")
			}
			if got.ID == "" {
				t.Error("ID: expected non-empty UUID, got empty string")
			}
			if got.UserID != tt.userID {
				t.Errorf("UserID: want %q, got %q", tt.userID, got.UserID)
			}
			if got.ItemID != itemID {
				t.Errorf("ItemID: want %q, got %q", itemID, got.ItemID)
			}
			if got.Quantity != tt.quantity {
				t.Errorf("Quantity: want %d, got %d", tt.quantity, got.Quantity)
			}
			if got.Source != "manual" {
				t.Errorf("Source: want %q, got %q", "manual", got.Source)
			}
			if got.PurchasedAt != nil {
				t.Errorf("PurchasedAt: want nil, got %v", got.PurchasedAt)
			}
		})
	}
}

func TestRemoveItem(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, deps testDeps, ctx context.Context) string
		expectError error
	}{
		{
			name: "removes existing item",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) string {
				itemID := createTestItem(t, deps, ctx, "user-1", "prod-1", "Eggs")
				got, err := deps.shopping.AddManualItem(ctx, "user-1", itemID, 2)
				if err != nil {
					t.Fatalf("AddManualItem: %v", err)
				}
				return got.ID
			},
		},
		{
			name: "not found returns ErrItemNotFound",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) string {
				return "no-such-id"
			},
			expectError: shopping.ErrItemNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestStore(t)
			ctx := context.Background()
			id := tt.setup(t, deps, ctx)
			err := deps.shopping.RemoveItem(ctx, id)
			if tt.expectError != nil {
				if !errors.Is(err, tt.expectError) {
					t.Errorf("error: want %v, got %v", tt.expectError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RemoveItem: %v", err)
			}
		})
	}
}

func TestMarkPurchased(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, deps testDeps, ctx context.Context) string
		expectError error
	}{
		{
			name: "sets purchased_at on existing item",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) string {
				itemID := createTestItem(t, deps, ctx, "user-1", "prod-1", "Bread")
				got, err := deps.shopping.AddManualItem(ctx, "user-1", itemID, 1)
				if err != nil {
					t.Fatalf("AddManualItem: %v", err)
				}
				return got.ID
			},
		},
		{
			name: "not found returns ErrItemNotFound",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) string {
				return "no-such-id"
			},
			expectError: shopping.ErrItemNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestStore(t)
			ctx := context.Background()
			id := tt.setup(t, deps, ctx)
			err := deps.shopping.MarkPurchased(ctx, id)
			if tt.expectError != nil {
				if !errors.Is(err, tt.expectError) {
					t.Errorf("error: want %v, got %v", tt.expectError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("MarkPurchased: %v", err)
			}
			items, err := deps.shopping.ListManualItems(ctx, "user-1")
			if err != nil {
				t.Fatalf("ListManualItems: %v", err)
			}
			for _, item := range items {
				if item.ID == id {
					t.Errorf("purchased item %q still appears in unpurchased list", id)
				}
			}
		})
	}
}

func TestListManualItems(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		setup       func(t *testing.T, deps testDeps, ctx context.Context)
		expectCount int
	}{
		{
			name:        "empty list",
			userID:      "user-1",
			setup:       func(t *testing.T, deps testDeps, ctx context.Context) {},
			expectCount: 0,
		},
		{
			name:   "returns only unpurchased manual items",
			userID: "user-1",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) {
				itemID := createTestItem(t, deps, ctx, "user-1", "prod-1", "Cheese")
				item1, err := deps.shopping.AddManualItem(ctx, "user-1", itemID, 1)
				if err != nil {
					t.Fatalf("AddManualItem: %v", err)
				}
				if _, err = deps.shopping.AddManualItem(ctx, "user-1", itemID, 2); err != nil {
					t.Fatalf("AddManualItem: %v", err)
				}
				if err := deps.shopping.MarkPurchased(ctx, item1.ID); err != nil {
					t.Fatalf("MarkPurchased: %v", err)
				}
			},
			expectCount: 1,
		},
		{
			name:   "does not return items for other users",
			userID: "user-1",
			setup: func(t *testing.T, deps testDeps, ctx context.Context) {
				itemIDUser1 := createTestItem(t, deps, ctx, "user-1", "prod-1", "Yogurt")
				itemIDUser2 := createTestItem(t, deps, ctx, "user-2", "prod-2", "Butter")
				if _, err := deps.shopping.AddManualItem(ctx, "user-1", itemIDUser1, 1); err != nil {
					t.Fatalf("AddManualItem user-1: %v", err)
				}
				if _, err := deps.shopping.AddManualItem(ctx, "user-2", itemIDUser2, 1); err != nil {
					t.Fatalf("AddManualItem user-2: %v", err)
				}
			},
			expectCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestStore(t)
			ctx := context.Background()
			tt.setup(t, deps, ctx)
			got, err := deps.shopping.ListManualItems(ctx, tt.userID)
			if err != nil {
				t.Fatalf("ListManualItems: %v", err)
			}
			if len(got) != tt.expectCount {
				t.Fatalf("count: want %d, got %d", tt.expectCount, len(got))
			}
			for _, item := range got {
				if item.PurchasedAt != nil {
					t.Errorf("item %q: PurchasedAt should be nil for unpurchased items", item.ID)
				}
				if item.Source != "manual" {
					t.Errorf("item %q: Source want %q, got %q", item.ID, "manual", item.Source)
				}
				if item.UserID != tt.userID {
					t.Errorf("item %q: UserID want %q, got %q", item.ID, tt.userID, item.UserID)
				}
			}
		})
	}
}
