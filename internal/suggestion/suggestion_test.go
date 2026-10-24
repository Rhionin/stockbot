package suggestion_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/suggestion"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// newTestConsumptionLog opens an in-memory SQLite database, applies all migrations,
// and returns a suggestion ConsumptionLog.
func newTestConsumptionLog(t *testing.T) (*suggestion.ConsumptionLog, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return suggestion.NewConsumptionLog(conn), conn
}

// createTestItem creates a product and item in the DB and returns the item ID.
func primaryScope(t *testing.T, conn *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func createTestItem(t *testing.T, conn *sql.DB, ctx context.Context, scope tenant.Scope, productID, productName string) string {
	t.Helper()
	catalog := product.NewCatalog(conn)
	p := product.Product{ID: productID, Name: productName, Category: "Test"}
	if err := catalog.CreateProduct(ctx, p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	pantry := inventory.NewPantry(conn)
	item, err := pantry.GetOrCreateItem(ctx, scope, productID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	return item.ID
}

// --------------------------------------------------------------------------
// TestInsertConsumptionEvent
// --------------------------------------------------------------------------

func TestInsertConsumptionEvent(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name            string
		event           func(itemID string) suggestion.ConsumptionEvent
		wantIDGenerated bool
		wantScanEntryID bool
	}{
		{
			name: "explicit ID, no scan entry",
			event: func(itemID string) suggestion.ConsumptionEvent {
				return suggestion.ConsumptionEvent{
					ID:         "evt-1",
					ItemID:     itemID,
					ConsumedAt: now,
				}
			},
			wantIDGenerated: false,
			wantScanEntryID: false,
		},
		{
			name: "generate UUID when ID empty",
			event: func(itemID string) suggestion.ConsumptionEvent {
				return suggestion.ConsumptionEvent{
					ItemID:     itemID,
					ConsumedAt: now,
				}
			},
			wantIDGenerated: true,
			wantScanEntryID: false,
		},
		{
			name: "with scan entry ID",
			event: func(itemID string) suggestion.ConsumptionEvent {
				scanID := "scan-abc"
				return suggestion.ConsumptionEvent{
					ItemID:      itemID,
					ConsumedAt:  now,
					ScanEntryID: &scanID,
				}
			},
			wantIDGenerated: true,
			wantScanEntryID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, conn := newTestConsumptionLog(t)
			ctx := context.Background()
			scope := primaryScope(t, conn)
			itemID := createTestItem(t, conn, ctx, scope, "prod-1", "Milk")

			evt := tt.event(itemID)
			originalID := evt.ID

			inserted, err := log.InsertConsumptionEvent(ctx, scope, evt)
			if err != nil {
				t.Fatalf("InsertConsumptionEvent: %v", err)
			}
			if inserted == nil {
				t.Fatal("expected inserted event, got nil")
			}

			// Verify ID
			if tt.wantIDGenerated {
				if inserted.ID == "" {
					t.Error("expected generated UUID, got empty string")
				}
			} else {
				if inserted.ID != originalID {
					t.Errorf("ID: want %q, got %q", originalID, inserted.ID)
				}
			}

			// Verify fields round-trip
			if inserted.ItemID != itemID {
				t.Errorf("ItemID: want %q, got %q", itemID, inserted.ItemID)
			}

			// Verify ScanEntryID
			if tt.wantScanEntryID && inserted.ScanEntryID == nil {
				t.Error("ScanEntryID: want non-nil, got nil")
			}
			if !tt.wantScanEntryID && inserted.ScanEntryID != nil {
				t.Errorf("ScanEntryID: want nil, got %q", *inserted.ScanEntryID)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestListConsumptionEvents
// --------------------------------------------------------------------------

func TestListConsumptionEvents(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		setup       func(t *testing.T, log *suggestion.ConsumptionLog, conn *sql.DB, ctx context.Context) string // returns itemID
		expectCount int
		expectOrder []time.Time // consumed_at in expected ascending order
	}{
		{
			name: "empty — no events",
			setup: func(t *testing.T, log *suggestion.ConsumptionLog, conn *sql.DB, ctx context.Context) string {
				return createTestItem(t, conn, ctx, primaryScope(t, conn), "prod-1", "Milk")
			},
			expectCount: 0,
		},
		{
			name: "single event",
			setup: func(t *testing.T, log *suggestion.ConsumptionLog, conn *sql.DB, ctx context.Context) string {
				scope := primaryScope(t, conn)
				itemID := createTestItem(t, conn, ctx, scope, "prod-1", "Milk")
				_, err := log.InsertConsumptionEvent(ctx, scope, suggestion.ConsumptionEvent{
					ItemID:     itemID,
					ConsumedAt: now,
				})
				if err != nil {
					t.Fatalf("InsertConsumptionEvent: %v", err)
				}
				return itemID
			},
			expectCount: 1,
			expectOrder: []time.Time{now},
		},
		{
			name: "multiple events ordered ascending by consumed_at",
			setup: func(t *testing.T, log *suggestion.ConsumptionLog, conn *sql.DB, ctx context.Context) string {
				scope := primaryScope(t, conn)
				itemID := createTestItem(t, conn, ctx, scope, "prod-1", "Milk")
				// Insert out of order intentionally
				times := []time.Time{
					now.Add(2 * 24 * time.Hour),
					now,
					now.Add(1 * 24 * time.Hour),
				}
				for _, ts := range times {
					_, err := log.InsertConsumptionEvent(ctx, scope, suggestion.ConsumptionEvent{
						ItemID:     itemID,
						ConsumedAt: ts,
					})
					if err != nil {
						t.Fatalf("InsertConsumptionEvent: %v", err)
					}
				}
				return itemID
			},
			expectCount: 3,
			expectOrder: []time.Time{
				now,
				now.Add(1 * 24 * time.Hour),
				now.Add(2 * 24 * time.Hour),
			},
		},
		{
			name: "filters by item — other item's events not returned",
			setup: func(t *testing.T, log *suggestion.ConsumptionLog, conn *sql.DB, ctx context.Context) string {
				scope := primaryScope(t, conn)
				itemID1 := createTestItem(t, conn, ctx, scope, "prod-1", "Milk")
				itemID2 := createTestItem(t, conn, ctx, scope, "prod-2", "Bread")

				// Add events to both items
				for _, id := range []string{itemID1, itemID2} {
					_, err := log.InsertConsumptionEvent(ctx, scope, suggestion.ConsumptionEvent{
						ItemID:     id,
						ConsumedAt: now,
					})
					if err != nil {
						t.Fatalf("InsertConsumptionEvent: %v", err)
					}
				}
				return itemID1 // only query itemID1
			},
			expectCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, conn := newTestConsumptionLog(t)
			ctx := context.Background()
			itemID := tt.setup(t, log, conn, ctx)
			scope := primaryScope(t, conn)

			events, err := log.ListConsumptionEvents(ctx, scope, itemID)
			if err != nil {
				t.Fatalf("ListConsumptionEvents: %v", err)
			}
			if len(events) != tt.expectCount {
				t.Fatalf("expected %d events, got %d", tt.expectCount, len(events))
			}

			// Verify all events belong to the queried item
			for _, e := range events {
				if e.ItemID != itemID {
					t.Errorf("ItemID: want %q, got %q", itemID, e.ItemID)
				}
			}

			// Verify ascending order by consumed_at
			for i := 0; i < len(events)-1; i++ {
				if events[i].ConsumedAt.After(events[i+1].ConsumedAt) {
					t.Errorf("events not sorted ascending: [%d] %v > [%d] %v",
						i, events[i].ConsumedAt, i+1, events[i+1].ConsumedAt)
				}
			}

			// Verify expected timestamps when provided
			for i, wantTime := range tt.expectOrder {
				if i >= len(events) {
					break
				}
				// Truncate to second precision to avoid sub-second SQLite rounding differences
				if !events[i].ConsumedAt.Truncate(time.Second).Equal(wantTime.Truncate(time.Second)) {
					t.Errorf("[%d] ConsumedAt: want ~%v, got %v", i, wantTime, events[i].ConsumedAt)
				}
			}
		})
	}
}

func TestConsumptionEventsStayInTheirHousehold(t *testing.T) {
	log, conn := newTestConsumptionLog(t)
	ctx := context.Background()
	home := primaryScope(t, conn)
	if _, err := log.InsertConsumptionEvent(ctx, tenant.Scope{}, suggestion.ConsumptionEvent{ItemID: "missing"}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("missing household: %v", err)
	}
	const cedarID = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := conn.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedarID); err != nil {
		t.Fatal(err)
	}
	cedar, err := tenant.New(cedarID)
	if err != nil {
		t.Fatal(err)
	}
	itemID := createTestItem(t, conn, ctx, cedar, "cedar-oats", "Cedar oats")
	inserted, err := log.InsertConsumptionEvent(ctx, cedar, suggestion.ConsumptionEvent{
		ID:         "cedar-ate",
		ItemID:     itemID,
		ConsumedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	homeEvents, err := log.ListConsumptionEvents(ctx, home, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(homeEvents) != 0 {
		t.Fatalf("primary household read cedar's event: %+v", homeEvents)
	}
	homeTimes, err := log.ListConsumedAtByItems(ctx, home, []string{itemID})
	if err != nil {
		t.Fatal(err)
	}
	if len(homeTimes[itemID]) != 0 {
		t.Fatalf("primary household counted cedar's event: %+v", homeTimes)
	}
	if _, err := log.InsertConsumptionEvent(ctx, home, suggestion.ConsumptionEvent{
		ID:         inserted.ID,
		ItemID:     itemID,
		ConsumedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}); err == nil {
		t.Fatal("primary household replaced cedar's consumption event")
	}
	cedarEvents, err := log.ListConsumptionEvents(ctx, cedar, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cedarEvents) != 1 || cedarEvents[0].ID != "cedar-ate" || !cedarEvents[0].ConsumedAt.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cedar event = %+v", cedarEvents)
	}
}

func TestListConsumedAtByItems(t *testing.T) {
	log, conn := newTestConsumptionLog(t)
	ctx := context.Background()
	scope := primaryScope(t, conn)
	empty, err := log.ListConsumedAtByItems(ctx, scope, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty %#v err %v", empty, err)
	}
	itemID := createTestItem(t, conn, ctx, scope, "prod-times", "Timed oats")
	when := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if _, err := log.InsertConsumptionEvent(ctx, scope, suggestion.ConsumptionEvent{ItemID: itemID, ConsumedAt: when}); err != nil {
		t.Fatal(err)
	}
	got, err := log.ListConsumedAtByItems(ctx, scope, []string{itemID, "item-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[itemID]) != 1 || !got[itemID][0].Equal(when) {
		t.Fatalf("times %#v", got)
	}
	if _, ok := got["item-2"]; ok {
		t.Fatal("item-2 had events")
	}
}
