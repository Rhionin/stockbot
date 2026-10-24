package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

// TestExternalProductPersistenceBugCondition surfaces counterexamples that
// demonstrate the external-product-persistence bug on the UNFIXED code.
//
// Bug: when a barcode is resolved only by the external Open Food Facts lookup
// (Tier 3), the resolved product is never written to the products table. The
// scan entry records product_id = <barcode>, committing a stock-in creates an
// items row referencing that product_id, but because no matching products row
// exists, every inventory query's inner JOIN products drops the item.
//
// Property 1 (Bug Condition): External Product Is Persisted And Visible.
//
// Each case here is EXPECTED TO FAIL on the unfixed code — that failure is the
// counterexample that confirms the bug exists. When the persistence fix lands,
// these same tests encode the expected (correct) behavior and should pass.
//
// The exploration is scoped to the confirmed counterexample barcode
// 011110728227 plus a small set of fake-resolved barcodes rather than the full
// string domain, because this is a deterministic, reproducible defect.
//
// Verification is done through the HTTP contract using an httptest server bound
// to the same DB/catalog/fake as the handler under test. We drive the follow-up
// requests directly (rather than via exchanges()) so a failing assertion is
// reported cleanly through env.T as a counterexample instead of panicking.
func TestExternalProductPersistenceBugCondition(t *testing.T) {
	// The confirmed counterexample from the field: Kroger lemon juice.
	const lemonJuiceBarcode = "011110728227"
	const userID = "user-1"

	t.Run("external scan then commit then inventory (primary counterexample)", func(t *testing.T) {
		// Confirms Req 1.1, 1.2, 1.3.
		harness := newExternalHarness(t)
		harness.env.OpenFoodFacts.Seed(lemonJuiceBarcode, &product.ProductSummary{
			ID: lemonJuiceBarcode, Name: "Lemon Juice", Category: "Beverages", UnitOfMeasure: "bottle",
		})

		// External lookup resolves the product: scan is pending with the ID.
		created := harness.createScan(lemonJuiceBarcode, userID)
		if got := created["status"]; got != "pending" {
			t.Errorf("scan status: want pending, got %v", got)
		}
		if got := created["productId"]; got != lemonJuiceBarcode {
			t.Errorf("scan productId: want %s, got %v", lemonJuiceBarcode, got)
		}
		scanID, _ := created["id"].(string)

		harness.commit(scanID)

		// EXPECTED-ON-FIXED-CODE: the externally-resolved item is present in
		// inventory with its resolved name. FAILS on unfixed code where
		// GET /api/inventory returns [].
		inv := harness.inventory(userID)
		if len(inv) == 0 {
			t.Fatalf("COUNTEREXAMPLE: GET /api/inventory returned [] after committing an "+
				"externally-resolved stock-in of %s; no products row was written on Tier-3 "+
				"resolution, so the inner JOIN products dropped the item", lemonJuiceBarcode)
		}
		if name := productName(inv[0]); name != "Lemon Juice" {
			t.Errorf("inventory[0] product name: want Lemon Juice, got %q", name)
		}
	})

	t.Run("external scan then product not retrievable by ID", func(t *testing.T) {
		// Confirms Req 1.1 / 2.5 gap. The product-by-ID / catalog path is exposed
		// via GET /api/products (ListProducts). On unfixed code the externally
		// resolved product was never persisted, so it is not retrievable.
		harness := newExternalHarness(t)
		harness.env.OpenFoodFacts.Seed(lemonJuiceBarcode, &product.ProductSummary{
			ID: lemonJuiceBarcode, Name: "Lemon Juice", Category: "Beverages", UnitOfMeasure: "bottle",
		})

		created := harness.createScan(lemonJuiceBarcode, userID)
		if got := created["productId"]; got != lemonJuiceBarcode {
			t.Errorf("scan productId: want %s, got %v", lemonJuiceBarcode, got)
		}

		// EXPECTED-ON-FIXED-CODE: the resolved product is in the catalog and
		// retrievable by its ID. FAILS on unfixed code where the catalog is empty.
		products := harness.products()
		found := false
		for _, p := range products {
			if p["id"] == lemonJuiceBarcode {
				found = true
				if p["name"] != "Lemon Juice" {
					t.Errorf("catalog product name: want Lemon Juice, got %v", p["name"])
				}
			}
		}
		if !found {
			t.Fatalf("COUNTEREXAMPLE: product %s not retrievable via GET /api/products after an "+
				"external-only resolution; Tier-3 lookup returned it without persisting a products row",
				lemonJuiceBarcode)
		}
	})

	t.Run("inventory vs instances inconsistency after external commit", func(t *testing.T) {
		// Confirms Req 1.4. After commit, the instances endpoint returns the live
		// instance while GET /api/inventory omits the parent item.
		harness := newExternalHarness(t)
		harness.env.OpenFoodFacts.Seed(lemonJuiceBarcode, &product.ProductSummary{
			ID: lemonJuiceBarcode, Name: "Lemon Juice", Category: "Beverages", UnitOfMeasure: "bottle",
		})

		created := harness.createScan(lemonJuiceBarcode, userID)
		scanID, _ := created["id"].(string)
		harness.commit(scanID)

		// The item id is not exposed through GET /api/inventory on unfixed code
		// (the parent is dropped), so read it directly from the shared DB to drive
		// the instances endpoint. This DB read is a last resort: the item id is
		// not obtainable via any API while the parent is invisible.
		var itemID string
		if err := harness.env.DB.QueryRowContext(context.Background(),
			`SELECT id FROM items WHERE user_id = ? AND product_id = ?`,
			userID, lemonJuiceBarcode,
		).Scan(&itemID); err != nil {
			t.Fatalf("query item id: %v", err)
		}

		// The instances endpoint does NOT join products, so the live instance is
		// returned on both unfixed and fixed code.
		instances := harness.instances(itemID)
		if len(instances) != 1 {
			t.Fatalf("instances endpoint: want 1 live instance, got %d", len(instances))
		}

		// EXPECTED-ON-FIXED-CODE: the parent item is also visible in the inventory
		// list, keeping the two views consistent. FAILS on unfixed code.
		inv := harness.inventory(userID)
		if len(inv) == 0 {
			t.Fatalf("COUNTEREXAMPLE: inventory/instances inconsistency — "+
				"GET /api/inventory/%s/instances returns the live instance while "+
				"GET /api/inventory omits the parent item (returns [])", itemID)
		}
		if id := itemID; id != itemIDOf(inv[0]) {
			t.Errorf("inventory[0] item id: want %s, got %s", itemID, itemIDOf(inv[0]))
		}
	})

	t.Run("pre-existing orphan omitted from inventory", func(t *testing.T) {
		// Confirms Req 1.5 / 2.6. Seed items + item_instances whose product_id has
		// no products row (an orphan created before the fix).
		harness := newExternalHarness(t)

		if _, err := harness.env.DB.ExecContext(context.Background(),
			`INSERT INTO items (id, user_id, product_id) VALUES ('orphan-item', ?, ?)`,
			userID, lemonJuiceBarcode,
		); err != nil {
			t.Fatalf("insert orphan item: %v", err)
		}
		if _, err := harness.env.DB.ExecContext(context.Background(),
			`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('orphan-inst', 'orphan-item', CURRENT_TIMESTAMP)`,
		); err != nil {
			t.Fatalf("insert orphan instance: %v", err)
		}

		// Migration 002 is a one-time STARTUP repair: it backfills products rows for
		// orphans that already exist in the DB (design Property 4:
		// FOR ALL item WHERE itemHasNoProductsRow(item) DO runRepair). The harness
		// runs RunMigrations once at construction — against an EMPTY DB, before this
		// orphan is seeded — so migration 002 is already recorded in
		// schema_migrations and a plain re-invocation would skip it. To faithfully
		// simulate startup repair running against a pre-existing orphan (seed →
		// repair → observe), we clear the 002 record so RunMigrations re-applies the
		// backfill against the now-seeded orphan before inventory is observed.
		if _, err := harness.env.DB.ExecContext(context.Background(),
			`DELETE FROM schema_migrations WHERE filename = '002_backfill_orphaned_products.sql'`,
		); err != nil {
			t.Fatalf("reset migration 002 record: %v", err)
		}
		if err := app.RunMigrations(harness.env.DB); err != nil {
			t.Fatalf("RunMigrations (startup repair against pre-existing orphan): %v", err)
		}

		// EXPECTED-ON-FIXED-CODE: the repair migration backfills a products row so
		// the orphaned item appears. FAILS on unfixed code where the inner JOIN
		// drops it.
		inv := harness.inventory(userID)
		if len(inv) == 0 {
			t.Fatalf("COUNTEREXAMPLE: a pre-existing orphaned item (items row 'orphan-item' whose "+
				"product_id %s has no products row) is omitted from GET /api/inventory (returns [])",
				lemonJuiceBarcode)
		}
		if id := itemIDOf(inv[0]); id != "orphan-item" {
			t.Errorf("inventory[0] item id: want orphan-item, got %s", id)
		}
	})
}

// externalHarness wires an httptest server to the same DB/catalog/fake so
// follow-up HTTP requests can be verified with clean Go assertions.
type externalHarness struct {
	t      *testing.T
	env    testEnv
	server *httptest.Server
}

func newExternalHarness(t *testing.T) *externalHarness {
	t.Helper()
	handler, env := setupTestWithDB(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &externalHarness{
		t:      t,
		env:    env,
		server: server,
	}
}

// createScan POSTs a stock-in scan for the barcode and returns the decoded body.
func (h *externalHarness) createScan(barcode, userID string) map[string]any {
	h.t.Helper()
	body := `{"barcode":"` + barcode + `","direction":"stock_in","unitCount":1,"userId":"` + userID + `"}`
	res := h.do(http.MethodPost, "/api/scans", body)
	if res.StatusCode != http.StatusCreated {
		h.t.Fatalf("POST /api/scans: want 201, got %d (body: %s)", res.StatusCode, res.rawBody)
	}
	return res.object()
}

// commit commits the scan entry and asserts it transitions to committed.
func (h *externalHarness) commit(scanID string) {
	h.t.Helper()
	res := h.do(http.MethodPost, "/api/scans/"+scanID+"/commit", "")
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("POST /api/scans/%s/commit: want 200, got %d (body: %s)", scanID, res.StatusCode, res.rawBody)
	}
	if status := res.object()["status"]; status != "committed" {
		h.t.Fatalf("commit status: want committed, got %v", status)
	}
}

// inventory GETs the inventory list and returns the decoded array.
func (h *externalHarness) inventory(userID string) []map[string]any {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/inventory", "")
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /api/inventory: want 200, got %d", res.StatusCode)
	}
	return res.array()
}

// instances GETs the live instances for an item and returns the decoded array.
func (h *externalHarness) instances(itemID string) []map[string]any {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/inventory/"+itemID+"/instances", "")
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /api/inventory/%s/instances: want 200, got %d", itemID, res.StatusCode)
	}
	return res.array()
}

// products GETs the catalog list and returns the decoded array.
func (h *externalHarness) products() []map[string]any {
	h.t.Helper()
	res := h.do(http.MethodGet, "/api/products", "")
	if res.StatusCode != http.StatusOK {
		h.t.Fatalf("GET /api/products: want 200, got %d", res.StatusCode)
	}
	return res.array()
}

// httpResult holds a decoded HTTP response for assertion helpers.
type httpResult struct {
	t          *testing.T
	StatusCode int
	rawBody    []byte
}

func (h *externalHarness) do(method, path, body string) httpResult {
	h.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("build request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read response body %s %s: %v", method, path, err)
	}
	return httpResult{t: h.t, StatusCode: resp.StatusCode, rawBody: raw}
}

func (r httpResult) object() map[string]any {
	r.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.rawBody, &m); err != nil {
		r.t.Fatalf("decode object body: %v (body: %s)", err, r.rawBody)
	}
	return m
}

func (r httpResult) array() []map[string]any {
	r.t.Helper()
	// A JSON null (nil slice serialized) decodes as an empty list here.
	var arr []map[string]any
	if len(r.rawBody) == 0 || string(r.rawBody) == "null" {
		return nil
	}
	if err := json.Unmarshal(r.rawBody, &arr); err != nil {
		r.t.Fatalf("decode array body: %v (body: %s)", err, r.rawBody)
	}
	return arr
}

// productName extracts inventory[i].item.product.name from a decoded item.
func productName(inventoryItem map[string]any) string {
	item, _ := inventoryItem["item"].(map[string]any)
	prod, _ := item["product"].(map[string]any)
	name, _ := prod["name"].(string)
	return name
}

// itemIDOf extracts inventory[i].item.id from a decoded inventory item.
func itemIDOf(inventoryItem map[string]any) string {
	item, _ := inventoryItem["item"].(map[string]any)
	id, _ := item["id"].(string)
	return id
}

// TestExternalPersistencePreservation locks in the behavior that the persistence
// fix must NOT regress: a stock-in committed for a product that is ALREADY
// persisted still creates the item and shows it in GET /api/inventory ordered by
// product name.
//
// Property 2 (Preservation): Non-External Resolutions Unchanged.
//
// This is a preservation case on the UNFIXED code — it MUST PASS as-is,
// confirming the already-persisted-product path (Tier 1/Tier 2 style, where a
// products row already exists) is unaffected by the fix.
//
// Confirms Req 3.4, 3.5.
func TestExternalPersistencePreservation(t *testing.T) {
	now := time.Now()

	tests := []handlerTestCase{
		{
			name: "already-persisted product: committed stock-in visible and ordered by name",
			setup: func(env testEnv) {
				// Two pre-seeded products; "Apple Juice" sorts before "Zucchini".
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-zucchini", Name: "Zucchini", Category: "Produce", UnitOfMeasure: "each",
				}); err != nil {
					env.T.Fatalf("CreateProduct zucchini: %v", err)
				}
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-apple", Name: "Apple Juice", Category: "Beverages", UnitOfMeasure: "bottle",
				}); err != nil {
					env.T.Fatalf("CreateProduct apple: %v", err)
				}

				// An existing item for the second product, so ordering-by-name is observable.
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-apple', 'user-1', 'prod-apple')`,
				); err != nil {
					env.T.Fatalf("insert apple item: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-apple', 'item-apple', CURRENT_TIMESTAMP)`,
				); err != nil {
					env.T.Fatalf("insert apple instance: %v", err)
				}

				// A pending stock-in scan for the already-persisted zucchini product.
				scanQueue := scan.NewQueue(env.DB)
				productID := "prod-zucchini"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-zucchini", UserID: "user-1", Barcode: "000000000001",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanQueue.CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-zucchini/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// Both already-persisted items appear, ordered by product name.
					{path: "$[0].item.product.name", value: "Apple Juice"},
					{path: "$[1].item.product.name", value: "Zucchini"},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}
