package server

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestInventoryStockMatchesScanLedger(t *testing.T) {
	expiresAt := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	tests := []handlerTestCase{
		{
			name: "opening records units and writes no usage",
			setup: func(env testEnv) {
				seedShelfItem(env, "prod-open", "Oats", "item-open")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/item-open/instances",
				body:           `{"expiresAt":"` + expiresAt.Format(time.RFC3339) + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.itemId", value: "item-open"},
				},
			},
			afterRequest: func(env testEnv) {
				insertPendingScan(env, "scan-open-in", "prod-open", scan.StockIn)
				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/scans/scan-open-in/commit",
					expectedStatus: http.StatusOK,
				})(env)
				if env.T.Failed() {
					return
				}
				assertShelf(env.T, env.DB, "item-open", "prod-open", shelfState{
					Units: []heldUnit{
						{ItemID: "item-open", ExpiresAt: "2026-12-01T00:00:00Z", OnHand: true},
						{ItemID: "item-open", OnHand: true},
					},
					StockIns:    []string{},
					Consumption: []consumptionRow{},
				})

				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/inventory/item-open/stock-out",
					expectedStatus: http.StatusOK,
				})(env)
				insertPendingScan(env, "scan-open-out", "prod-open", scan.StockOut)
				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/scans/scan-open-out/commit",
					expectedStatus: http.StatusOK,
				})(env)
				assertShelf(env.T, env.DB, "item-open", "prod-open", shelfState{
					Units: []heldUnit{
						{ItemID: "item-open", ExpiresAt: "2026-12-01T00:00:00Z", Reason: "consumed"},
						{ItemID: "item-open", Reason: "consumed"},
					},
					StockIns:    []string{},
					Consumption: []consumptionRow{},
				})
			},
		},
		{
			name: "after opening, stock-in and stock-out match a committed scan",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-after", "Milk", "item-after")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/item-after/instances",
				body:           `{}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.itemId", value: "item-after"},
				},
			},
			afterRequest: func(env testEnv) {
				insertPendingScan(env, "scan-after-in", "prod-after", scan.StockIn)
				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/scans/scan-after-in/commit",
					expectedStatus: http.StatusOK,
				})(env)
				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/inventory/item-after/stock-out",
					expectedStatus: http.StatusOK,
				})(env)
				insertPendingScan(env, "scan-after-out", "prod-after", scan.StockOut)
				exchanges(httpExchange{
					method:         "POST",
					path:           "/api/scans/scan-after-out/commit",
					expectedStatus: http.StatusOK,
				})(env)
				assertShelf(env.T, env.DB, "item-after", "prod-after", shelfState{
					Units: []heldUnit{
						{ItemID: "item-after", Reason: "consumed"},
						{ItemID: "item-after", Reason: "consumed"},
					},
					StockIns: []string{"prod-after", "prod-after"},
					Consumption: []consumptionRow{
						{ItemID: "item-after", ScanEntryID: ""},
						{ItemID: "item-after", ScanEntryID: "scan-after-out"},
					},
				})
			},
		},
		{
			name: "stock-out removes the earliest expiration",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-pick", "Yogurt", "item-pick")
				insertHeldInstance(env, "inst-later", "item-pick", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
				insertHeldInstance(env, "inst-soon", "item-pick", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
				insertHeldInstance(env, "inst-undated", "item-pick", time.Time{})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/item-pick/stock-out",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				assertShelf(env.T, env.DB, "item-pick", "prod-pick", shelfState{
					Units: []heldUnit{
						{ItemID: "item-pick", ExpiresAt: "2026-08-01T00:00:00Z", OnHand: true},
						{ItemID: "item-pick", OnHand: true},
						{ItemID: "item-pick", ExpiresAt: "2026-04-01T00:00:00Z", Reason: "consumed"},
					},
					StockIns: []string{},
					Consumption: []consumptionRow{
						{ItemID: "item-pick", ScanEntryID: ""},
					},
				})
				var removedID string
				if err := env.DB.QueryRowContext(context.Background(), `
					SELECT id FROM item_instances WHERE item_id = ? AND removed_at IS NOT NULL`,
					"item-pick",
				).Scan(&removedID); err != nil {
					env.T.Fatalf("removed instance: %v", err)
				}
				if removedID != "inst-soon" {
					env.T.Fatalf("removed instance = %q, want %q", removedID, "inst-soon")
				}
			},
		},
		{
			name: "removing one instance is the same stock-out",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-one", "Beans", "item-one")
				insertHeldInstance(env, "inst-keep", "item-one", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
				insertHeldInstance(env, "inst-drop", "item-one", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
			},
			httpExchange: httpExchange{
				method:         "DELETE",
				path:           "/api/inventory/instances/inst-drop",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				assertShelf(env.T, env.DB, "item-one", "prod-one", shelfState{
					Units: []heldUnit{
						{ItemID: "item-one", ExpiresAt: "2026-09-01T00:00:00Z", OnHand: true},
						{ItemID: "item-one", ExpiresAt: "2026-03-01T00:00:00Z", Reason: "consumed"},
					},
					StockIns: []string{},
					Consumption: []consumptionRow{
						{ItemID: "item-one", ScanEntryID: ""},
					},
				})
			},
		},
		{
			name: "stock-out at zero changes nothing",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-empty", "Rice", "item-empty")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/item-empty/stock-out",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
			},
			afterRequest: func(env testEnv) {
				assertShelf(env.T, env.DB, "item-empty", "prod-empty", shelfState{
					Units:       []heldUnit{},
					StockIns:    []string{},
					Consumption: []consumptionRow{},
				})
			},
		},
	}

	runHandlerTests(t, tests)
}

func TestManualStockOutLeavesGroupAndShoppingFillReadable(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "group detail and shopping fill succeed after a manual stock-out",
		setup: func(env testEnv) {
			finishOpening(env)
			seedShelfItem(env, "prod-oats", "Oats", "item-oats")
			insertHeldInstance(env, "inst-oats", "item-oats", time.Time{})
			insertGroup(env, "group-oats", "Oats")
			insertMember(env, "group-oats", "prod-oats")
		},
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/inventory/item-oats/stock-out",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			var at string
			if err := env.DB.QueryRow(`SELECT CAST(at AS TEXT) FROM stock_moves WHERE item_id = ?`, "item-oats").Scan(&at); err != nil {
				env.T.Fatalf("stock_moves.at: %v", err)
			}
			if strings.Contains(at, "m=") {
				env.T.Fatalf("stock_moves.at = %q", at)
			}
			exchanges(
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/groups/group-oats",
					expectedStatus: http.StatusOK,
				},
				httpExchange{
					method:         http.MethodPost,
					path:           "/api/shopping-list/fill",
					expectedStatus: http.StatusOK,
				},
			)(env)
		},
	}})
}

type heldUnit struct {
	ItemID    string
	ExpiresAt string
	OnHand    bool
	Reason    string
}

type consumptionRow struct {
	ItemID      string
	ScanEntryID string
}

type shelfState struct {
	Units       []heldUnit
	StockIns    []string
	Consumption []consumptionRow
}

func seedShelfItem(env testEnv, productID, name, itemID string) {
	env.T.Helper()
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: productID, Name: name, Category: "Pantry", UnitOfMeasure: "unit",
	}); err != nil {
		env.T.Fatalf("CreateProduct: %v", err)
	}
	if _, err := env.DB.ExecContext(context.Background(),
		`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`,
		itemID, productID,
	); err != nil {
		env.T.Fatalf("insert item: %v", err)
	}
}

func finishOpening(env testEnv) {
	env.T.Helper()
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if _, err := env.DB.ExecContext(context.Background(),
		`INSERT INTO household_settings (household_id, key, value)
		 SELECT id, 'onboarding_started_at', ? FROM households WHERE is_primary = 1`,
		stamp,
	); err != nil {
		env.T.Fatalf("finish opening: %v", err)
	}
}

func insertPendingScan(env testEnv, id, productID string, direction scan.ScanDirection) {
	env.T.Helper()
	queue := scan.NewQueue(env.DB)
	if _, err := queue.CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 1,
		ProductID: &productID,
		Status:    scan.Pending,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry: %v", err)
	}
}

func insertHeldInstance(env testEnv, id, itemID string, expires time.Time) {
	env.T.Helper()
	var expiresAt any
	if !expires.IsZero() {
		expiresAt = expires
	}
	if _, err := env.DB.ExecContext(context.Background(), `
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		id, itemID, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), expiresAt,
	); err != nil {
		env.T.Fatalf("insert instance: %v", err)
	}
}

func assertShelf(t *testing.T, db *sql.DB, itemID, productID string, want shelfState) {
	t.Helper()
	got := shelfState{
		Units:       listHeld(t, db, itemID),
		StockIns:    listStockIns(t, db, productID),
		Consumption: listConsumption(t, db, itemID),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shelf = %#v, want %#v", got, want)
	}
	var qty sql.NullInt64
	if err := db.QueryRowContext(context.Background(),
		`SELECT target_quantity FROM items WHERE id = ?`, itemID,
	).Scan(&qty); err != nil {
		t.Fatalf("target quantity: %v", err)
	}
	if qty.Valid {
		t.Fatalf("target_quantity = %d, want null", qty.Int64)
	}
}

func listHeld(t *testing.T, db *sql.DB, itemID string) []heldUnit {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT item_id, expires_at, removed_at IS NULL, COALESCE(removal_reason, '')
		FROM item_instances
		WHERE item_id = ?
		ORDER BY removed_at IS NULL DESC, COALESCE(expires_at, '9999-12-31'), id`,
		itemID,
	)
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	defer rows.Close()
	units := []heldUnit{}
	for rows.Next() {
		var unit heldUnit
		var expires sql.NullTime
		var onHand int
		if err := rows.Scan(&unit.ItemID, &expires, &onHand, &unit.Reason); err != nil {
			t.Fatalf("scan instance: %v", err)
		}
		unit.OnHand = onHand == 1
		if expires.Valid {
			unit.ExpiresAt = expires.Time.UTC().Format(time.RFC3339)
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list instances: %v", err)
	}
	return units
}

func listStockIns(t *testing.T, db *sql.DB, productID string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT product_id FROM stock_in_events WHERE product_id = ? ORDER BY id`,
		productID,
	)
	if err != nil {
		t.Fatalf("list stock-ins: %v", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan stock-in: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list stock-ins: %v", err)
	}
	return ids
}

func listConsumption(t *testing.T, db *sql.DB, itemID string) []consumptionRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT item_id, COALESCE(scan_entry_id, '')
		FROM consumption_events WHERE item_id = ?`,
		itemID,
	)
	if err != nil {
		t.Fatalf("list consumption: %v", err)
	}
	defer rows.Close()
	events := []consumptionRow{}
	for rows.Next() {
		var row consumptionRow
		if err := rows.Scan(&row.ItemID, &row.ScanEntryID); err != nil {
			t.Fatalf("scan consumption: %v", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list consumption: %v", err)
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].ScanEntryID < events[j].ScanEntryID
	})
	return events
}
