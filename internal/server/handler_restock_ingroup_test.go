package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func putGroupItemsGoal(t *testing.T, handler http.Handler, groupID string, value int) {
	t.Helper()
	exchangeJSON(t, handler, http.MethodPut, "/api/groups/"+groupID+"/target",
		fmt.Sprintf(`{"kind":"items","value":%d}`, value), http.StatusOK)
}

func TestRestockItemTargetInGroup(t *testing.T) {
	seedGrouped := func(env testEnv) (productID, itemID, groupID string) {
		productID = "prod-ingroup"
		itemID = "item-ingroup"
		groupID = "grp-ingroup"
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: productID, Name: "Grouped", UnitOfMeasure: "can",
		}); err != nil {
			env.T.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`, itemID, productID); err != nil {
			env.T.Fatal(err)
		}
		if _, err := env.DB.Exec(`
			INSERT INTO product_groups (id, user_id, name, name_key, rule)
			VALUES (?, 'user-1', 'The Group', 'the group', 'same_as_ran_out')`, groupID); err != nil {
			env.T.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO product_group_members (product_id, group_id) VALUES (?, ?)`, productID, groupID); err != nil {
			env.T.Fatal(err)
		}
		return productID, itemID, groupID
	}

	runHandlerTests(t, []handlerTestCase{
		{
			name: "put amount on grouped item",
			setup: func(env testEnv) {
				_, itemID, _ := seedGrouped(env)
				_ = itemID
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/restock/items/item-ingroup",
				body:           `{"kind":"amount","value":2}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "in_group"},
					{path: "$.error", value: "This product is in a group. Change what the group keeps on hand."},
					{path: "$.members.groupId", value: "grp-ingroup"},
					{path: "$.members.groupName", value: "The Group"},
				},
			},
			afterRequest: func(env testEnv) {
				var n int
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM item_targets WHERE item_id = 'item-ingroup'`).Scan(&n); err != nil {
					env.T.Fatal(err)
				}
				if n != 0 {
					env.T.Fatal("item_targets row written")
				}
			},
		},
		{
			name: "put months on grouped item",
			setup: func(env testEnv) { seedGrouped(env) },
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/restock/items/item-ingroup",
				body:           `{"kind":"months","value":4}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.members.groupId", value: "grp-ingroup"},
					{path: "$.members.groupName", value: "The Group"},
				},
			},
		},
		{
			name:  "value zero is allowed",
			setup: func(env testEnv) { seedGrouped(env) },
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/restock/items/item-ingroup",
				body:           `{"kind":"months","value":0}`,
				expectedStatus: http.StatusOK,
			},
		},
		{
			name:  "dont-restock allowed",
			setup: func(env testEnv) { seedGrouped(env) },
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/restock/items/item-ingroup/dont-restock",
				expectedStatus: http.StatusOK,
			},
		},
		{
			name:  "delete item target allowed",
			setup: func(env testEnv) {
				_, itemID, _ := seedGrouped(env)
				if _, err := env.DB.Exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ((SELECT id FROM households WHERE is_primary = 1), ?, 'months', 0)`, itemID); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodDelete,
				path:           "/api/restock/items/item-ingroup",
				expectedStatus: http.StatusOK,
			},
		},
		{
			name: "supply override put refused",
			setup: func(env testEnv) {
				seedGrouped(env)
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/products/prod-ingroup/supply-override",
				body:           `{"quantity":4}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "in_group"},
					{path: "$.members.groupName", value: "The Group"},
				},
			},
		},
		{
			name: "member dont-restock succeeds",
			setup: func(env testEnv) {
				_, _, groupID := seedGrouped(env)
				_ = groupID
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/groups/grp-ingroup/members/prod-ingroup/restock",
				body:           `{"noRestock":true}`,
				expectedStatus: http.StatusOK,
			},
		},
		{
			name: "ungrouped item succeeds",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-free", Name: "Free", UnitOfMeasure: "bag",
				}); err != nil {
					env.T.Fatal(err)
				}
				if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-free', 'user-1', 'prod-free')`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/restock/items/item-free",
				body:           `{"kind":"amount","value":2}`,
				expectedStatus: http.StatusOK,
			},
		},
	})
}