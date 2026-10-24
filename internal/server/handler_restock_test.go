package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestRestockErrorMapsHouseholdSentences(t *testing.T) {
	if restockError(nil) != nil {
		t.Fatal("nil error was reported")
	}
	for _, msg := range []string{
		"That item isn't in the household.",
		"That category isn't in the household.",
		"That group isn't in the household.",
	} {
		err := restockError(errors.New(msg))
		httpErr, ok := err.(*HTTPError)
		if !ok || httpErr.Code != http.StatusNotFound || httpErr.Message != msg {
			t.Fatalf("%s: %#v", msg, err)
		}
	}
	bad, ok := restockError(errors.New("Pick a number.")).(*HTTPError)
	if !ok || bad.Code != http.StatusBadRequest || bad.Message != "Pick a number." {
		t.Fatalf("capital sentence: %#v", bad)
	}
	internal, ok := restockError(errors.New("could not save the target")).(*HTTPError)
	if !ok || internal.Code != http.StatusInternalServerError {
		t.Fatalf("internal: %#v", internal)
	}
}

func TestRestockHandler(t *testing.T) {
	seed := func(env testEnv) {
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: "bananas", Name: "Bananas", UnitOfMeasure: "bunches",
		}); err != nil {
			env.T.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-bananas', 'user-1', 'bananas')`); err != nil {
			env.T.Fatal(err)
		}
	}

	runHandlerTests(t, []handlerTestCase{
		{
			name: "household default is three months and the starter categories exist",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.household.kind", value: "months"},
					{path: "$.household.value", value: float64(3)},
					{path: "$.categories[0].name", value: "Produce"},
					{path: "$.notRestocked", value: float64(0)},
				},
			},
		},
		{
			name:  "a household default of zero means don't restock",
			setup: seed,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/household",
				body:           `{"kind":"months","value":0}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.household.value", value: float64(0)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.items[0].name", value: "Bananas"},
					{path: "$.items[0].target.value", value: float64(0)},
					{path: "$.items[0].level", value: "household"},
					{path: "$.items[0].sourceLabel", value: "Household default"},
				},
			}),
		},
		{
			name:  "don't restock one item immediately and leave the category alone",
			setup: seed,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/restock/items/item-bananas/dont-restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.title", value: "Bananas won't be restocked"},
					{path: "$.detail", value: "Its target is now 0."},
					{path: "$.previous", value: nil},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.items[0].target.value", value: float64(0)},
					{path: "$.items[0].level", value: "item"},
					{path: "$.items[0].sourceLabel", value: "Set on this item"},
					{path: "$.items[0].setLabel", value: "Change"},
				},
			}),
		},
		{
			name: "a bad household target is rejected",
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/household",
				body:           `{"kind":"weeks","value":3}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name:  "a missing target value is rejected and changes nothing",
			setup: seed,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/household",
				body:           `{"kind":"months"}`,
				expectedStatus: http.StatusBadRequest,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.household.value", value: float64(3)},
					{path: "$.items[0].target.value", value: float64(3)},
					{path: "$.items[0].level", value: "household"},
				},
			}),
		},
		{
			name:  "a missing kind is rejected and zero is not assumed",
			setup: seed,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/items/item-bananas",
				body:           `{"value":0}`,
				expectedStatus: http.StatusBadRequest,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.items[0].level", value: "household"},
					{path: "$.items[0].target.value", value: float64(3)},
				},
			}),
		},
		{
			name: "don't restock drops the open shopping row and keeps a purchased one",
			setup: func(env testEnv) {
				seed(env)
				if _, err := env.DB.Exec(`
					INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, note) VALUES
						('row-open', 'user-1', 'item-bananas', 1, 'auto', 'replacing 1 you used'),
						('row-manual', 'user-1', 'item-bananas', 2, 'manual', '')`); err != nil {
					env.T.Fatal(err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, note, purchased_at)
					VALUES ('row-bought', 'user-1', 'item-bananas', 1, 'auto', 'replacing 1 you used', datetime('now'))`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/restock/items/item-bananas/dont-restock",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].source", value: "manual"},
						{path: "$[0].quantity", value: float64(2)},
						{path: "$[1]", absent: true},
					},
				})(env)
				var bought, open int
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = 'row-bought' AND purchased_at IS NOT NULL`).Scan(&bought); err != nil {
					env.T.Fatal(err)
				}
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = 'row-open'`).Scan(&open); err != nil {
					env.T.Fatal(err)
				}
				if bought != 1 || open != 0 {
					env.T.Fatalf("bought %d open %d", bought, open)
				}
			},
		},
		{
			name: "an unknown item is not found",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/restock/items/missing/dont-restock",
				expectedStatus: http.StatusNotFound,
			},
		},
	})
}

func TestRestockWrites(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "categories, groups, and suggestions round-trip",
			setup: func(env testEnv) {
				for _, row := range []struct{ id, name string }{{"bananas", "Bananas"}, {"lemons", "Lemons"}} {
					if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
						ID: row.id, Name: row.name, UnitOfMeasure: "each",
					}); err != nil {
						env.T.Fatal(err)
					}
					if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`, "item-"+row.id, row.id); err != nil {
						env.T.Fatal(err)
					}
				}
				if _, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Citrus", []string{"lemons"}, nil); err != nil {
					env.T.Fatal(err)
				}
				if _, err := env.DB.Exec(`INSERT INTO product_off_tags (product_id, categories_tags, food_groups_tags) VALUES ('bananas', '["en:fresh-fruits"]', '[]')`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				created := decodeID(env.T, exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/categories", `{"name":"Pantry extras"}`, http.StatusOK))
				other := decodeID(env.T, exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/categories", `{"name":"Odds"}`, http.StatusOK))
				exchangeJSON(env.T, handler, http.MethodPatch, "/api/restock/categories/"+created, `{"name":"Extras"}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/restock/categories/"+created+"/target", `{"kind":"amount","value":1}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/categories/"+created+"/preview", `{"kind":"months","value":0}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/restock/categories/"+created+"/target", `{"clear":true}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/categories/"+other+"/merge", `{"intoId":"`+created+`"}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/restock/categories/"+created, ``, http.StatusOK)

				exchangeJSON(env.T, handler, http.MethodPut, "/api/restock/items/item-bananas", `{"kind":"amount","value":4}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/restock/items/item-bananas", ``, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/items/item-bananas/categories", `{"categoryId":"`+produceID(env.T, handler)+`"}`, http.StatusOK)
				produce := produceID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/restock/items/item-bananas/categories/"+produce, ``, http.StatusOK)

				cards := exchangeJSON(env.T, handler, http.MethodGet, "/api/restock/suggestions", ``, http.StatusOK)
				if !json.Valid(cards) {
					env.T.Fatal("suggestions")
				}
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/suggestions/apply", `{"category":"Produce","itemIds":["item-bananas"]}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/suggestions/dismiss", `{"category":"Produce","itemIds":["item-lemons"]}`, http.StatusOK)

				var groupID string
				if err := env.DB.QueryRow(`SELECT id FROM product_groups WHERE name = 'Citrus'`).Scan(&groupID); err != nil {
					env.T.Fatal(err)
				}
				exchangeJSON(env.T, handler, http.MethodPost, "/api/restock/groups/"+groupID+"/preview", `{"kind":"months","value":0}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/restock/household", `{"kind":"amount","value":2}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodGet, "/api/restock/categories", ``, http.StatusOK)
			},
		},
	})
}

func decodeID(t *testing.T, body []byte) string {
	t.Helper()
	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &row); err != nil || row.ID == "" {
		t.Fatalf("id from %s: %v", body, err)
	}
	return row.ID
}

func produceID(t *testing.T, handler http.Handler) string {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/restock/categories", ``, http.StatusOK)
	var rows []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == "Produce" {
			return row.ID
		}
	}
	t.Fatal("Produce missing")
	return ""
}
