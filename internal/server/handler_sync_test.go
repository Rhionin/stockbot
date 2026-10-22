package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncSnapshot(t *testing.T) {
	const token = "sync-token-value-ok"
	gate := testHousehold(t, "pantry", "correct-password")
	runHandlerTests(t, []handlerTestCase{
		{
			name: "snapshot is hidden until a token is configured",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/sync/snapshot",
				expectedStatus: http.StatusNotFound,
				bodyContains:   []string{"Not found."},
			},
		},
		{
			name:    "missing token is rejected",
			options: []Option{WithSyncToken(token)},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/sync/snapshot",
				expectedStatus: http.StatusUnauthorized,
				bodyExcludes:   []string{token},
			},
		},
		{
			name:    "wrong token is rejected",
			options: []Option{WithSyncToken(token)},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/sync/snapshot",
				headers:        map[string]string{"Authorization": "Bearer nope"},
				expectedStatus: http.StatusUnauthorized,
			},
		},
		{
			name:    "an empty pantry is an empty snapshot",
			options: []Option{WithSyncToken(token)},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/sync/snapshot",
				headers:        map[string]string{"Authorization": "Bearer " + token},
				expectedStatus: http.StatusOK,
				bodyContains:   []string{`"products":[]`, `"groups":[]`, `"instances":[]`, `"moves":[]`, `"shoppingList":[]`},
				bodyExcludes:   []string{token},
			},
		},
		{
			name:    "the token cannot change products",
			options: []Option{WithSyncToken(token)},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				headers:        map[string]string{"Authorization": "Bearer " + token},
				body:           `{"name":"Nope"}`,
				expectedStatus: http.StatusForbidden,
				bodyContains:   []string{"This token only reads the sync snapshot."},
			},
		},
		{
			name:    "the token works on the public site without a session",
			options: []Option{WithSyncToken(token), WithHouseholdAuth(gate)},
			setup: func(env testEnv) {
				seedSyncSnapshot(env)
			},
			httpExchange: httpExchange{
				method: "GET",
				path:   "/api/sync/snapshot",
				headers: map[string]string{
					"Authorization":  "Bearer " + token,
					"X-Pantry-Entry": "public",
				},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.products[0].name", value: "Chickpeas"},
					{path: "$.products[0].barcodes[0]", value: "111"},
					{path: "$.products[0].targetQuantity", value: float64(4)},
					{path: "$.products[0].groupId", value: "g1"},
					{path: "$.products[1].name", value: "Oats"},
					{path: "$.products[1].barcodes[0]", value: "222"},
					{path: "$.groups[0].name", value: "Beans"},
					{path: "$.groups[0].memberIds[0]", value: "p-chick"},
					{path: "$.instances[0].id", value: "inst-1"},
					{path: "$.moves[0].direction", value: "in"},
					{path: "$.shoppingList[0].id", value: "line-open"},
					{path: "$.shoppingList[0].groupId", value: "g1"},
					{path: "$.removals[0].reason", value: "expired"},
					{path: "$.removals[0].expiresAt", value: "2026-06-01T00:00:00Z"},
					{path: "$.products[2].name", value: "Zucchini"},
					{path: "$.products[2].noRestock", value: true},
					{path: "$.products[2].netDimension", value: "volume"},
					{path: "$.groups[1].name", value: "Zest"},
					{path: "$.groups[1].quantityBase", value: float64(500)},
					{path: "$.groups[1].dimension", value: "volume"},
					{path: "$.groups[1].pinnedProductId", value: "p-zest"},
				},
				bodyExcludes: []string{token, "price", "line-done"},
			},
		},
	})
}

func TestSyncTokenOpensOnlyTheEventStream(t *testing.T) {
	const token = "sync-token-value-ok"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !syncAuthorized(r) {
			t.Errorf("event stream was not marked authorized")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	gate := syncGate(token, next)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("events status = %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/products", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	gate.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("products status = %d", rr.Code)
	}
}

func seedSyncSnapshot(env testEnv) {
	t := env.T
	stmts := []string{
		`INSERT INTO products (id, name, category, net_base_value, net_dimension, pack_count) VALUES
			('p-chick', 'Chickpeas', 'Canned', 400, 'mass', 6),
			('p-oats', 'Oats', 'Cereal', NULL, NULL, NULL)`,
		`INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES
			('111', 'p-oats', 'global', ''),
			('111', 'p-chick', 'user_override', 'user-1'),
			('222', 'p-oats', 'global', '')`,
		`INSERT INTO items (id, user_id, product_id, target_quantity) VALUES
			('item-chick', 'user-1', 'p-chick', 4),
			('item-oats', 'user-1', 'p-oats', NULL)`,
		`INSERT INTO product_groups (id, user_id, name, name_key, rule, window_months) VALUES
			('g1', 'user-1', 'Beans', 'beans', 'same_as_ran_out', 2)`,
		`INSERT INTO product_group_members (product_id, group_id, no_restock) VALUES ('p-chick', 'g1', 0)`,
		`INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source) VALUES
			('move-in', 'item-chick', 'p-chick', 'in', 2, '2026-01-02T15:04:05Z', 'manual'),
			('move-out', 'item-chick', 'p-chick', 'out', 1, '2026-02-02T15:04:05Z', 'scan')`,
		`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, stock_move_id) VALUES
			('inst-1', 'item-chick', '2026-01-02T15:04:05Z', '2026-06-01T00:00:00Z', 'move-in')`,
		`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, removed_at, removal_reason, stock_move_id, removed_by_move_id) VALUES
			('inst-old', 'item-chick', '2026-01-02T15:04:05Z', '2026-06-01T00:00:00Z', '2026-02-02T15:04:05Z', 'expired', 'move-in', 'move-out')`,
		`INSERT INTO products (id, name, category, unit_of_measure, net_base_value, net_dimension) VALUES
			('p-zest', 'Zucchini', 'Veg', 'bunch', 250, 'volume')`,
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-zest', 'user-1', 'p-zest')`,
		`INSERT INTO product_groups (id, user_id, name, name_key, rule, pinned_product_id, quantity_base_value, quantity_dimension) VALUES
			('g2', 'user-1', 'Zest', 'zest', 'favorite', 'p-zest', 500, 'volume')`,
		`INSERT INTO product_group_members (product_id, group_id, no_restock) VALUES ('p-zest', 'g2', 0)`,
		`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ('user-1', 'item-zest', 'months', 0)`,
		`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, group_id) VALUES
			('line-open', 'user-1', 'item-chick', 2, 'auto', 'g1')`,
		`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, purchased_at) VALUES
			('line-done', 'user-1', 'item-oats', 1, 'manual', '2026-03-01T00:00:00Z')`,
		`INSERT INTO item_deals (user_id, item_id, price_cents, label) VALUES
			('user-1', 'item-chick', 199, 'sale')`,
	}
	for _, stmt := range stmts {
		if _, err := env.DB.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
}
