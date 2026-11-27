package server

import (
	"net/http"
	"testing"
)

func seedGroupedItem(env testEnv) {
	for _, stmt := range []string{
		`INSERT INTO products (id, name, unit_of_measure) VALUES ('prod-beans', 'Cut Green Beans', 'can')`,
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-beans', 'user-1', 'prod-beans')`,
		`INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES ('g-beans', 'user-1', 'Green beans', 'green beans', 'same_as_ran_out')`,
		`INSERT INTO product_group_members (product_id, group_id) VALUES ('prod-beans', 'g-beans')`,
	} {
		if _, err := env.DB.Exec(stmt); err != nil {
			env.T.Fatal(err)
		}
	}
}

func seedUngroupedItem(env testEnv) {
	for _, stmt := range []string{
		`INSERT INTO products (id, name, unit_of_measure) VALUES ('prod-rice', 'Rice', 'bag')`,
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-rice', 'user-1', 'prod-rice')`,
	} {
		if _, err := env.DB.Exec(stmt); err != nil {
			env.T.Fatal(err)
		}
	}
}

func noItemTargetWritten() httpExchange {
	return httpExchange{
		method:         "GET",
		path:           "/api/restock",
		expectedStatus: http.StatusOK,
		assertions: []assertion{
			{path: "$.items[0].itemId", value: "item-beans"},
			{path: "$.items[0].level", value: "household"},
		},
	}
}

func TestRestockItemTargetRefusesGroupedProductInGroup(t *testing.T) {
	refused := []struct {
		name, method, path, body string
	}{
		{"amount above 0", "PUT", "/api/restock/items/item-beans", `{"kind":"amount","value":2}`},
		{"months above 0", "PUT", "/api/restock/items/item-beans", `{"kind":"months","value":4}`},
		{"supply override quantity", "PUT", "/api/products/prod-beans/supply-override", `{"quantity":4}`},
		{"supply override window", "PUT", "/api/products/prod-beans/supply-override", `{"windowMonths":2}`},
	}
	var tests []handlerTestCase
	for _, c := range refused {
		tests = append(tests, handlerTestCase{
			name:  c.name,
			setup: seedGroupedItem,
			httpExchange: httpExchange{
				method:         c.method,
				path:           c.path,
				body:           c.body,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "in_group"},
					{path: "$.error", value: "This product is in a group. Change what the group keeps on hand."},
					{path: "$.members.groupId", value: "g-beans"},
					{path: "$.members.groupName", value: "Green beans"},
				},
			},
			afterRequest: exchanges(noItemTargetWritten()),
		})
	}
	tests = append(tests,
		handlerTestCase{
			name:  "months 0 on a grouped product is don't restock",
			setup: seedGroupedItem,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/items/item-beans",
				body:           `{"kind":"months","value":0}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.level", value: "item"},
					{path: "$.target.value", value: float64(0)},
				},
			},
		},
		handlerTestCase{
			name:  "amount 0 on a grouped product is don't restock",
			setup: seedGroupedItem,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/items/item-beans",
				body:           `{"kind":"amount","value":0}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.level", value: "item"},
					{path: "$.target.value", value: float64(0)},
				},
			},
		},
		handlerTestCase{
			name:  "dont-restock on a grouped product succeeds",
			setup: seedGroupedItem,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/restock/items/item-beans/dont-restock",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/restock",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.items[0].level", value: "item"},
					{path: "$.items[0].target.value", value: float64(0)},
				},
			}, httpExchange{
				method:         "DELETE",
				path:           "/api/restock/items/item-beans",
				expectedStatus: http.StatusOK,
			}, noItemTargetWritten()),
		},
		handlerTestCase{
			name:  "member restock on the group page succeeds",
			setup: seedGroupedItem,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/groups/g-beans/members/prod-beans/restock",
				body:           `{"restock":false}`,
				expectedStatus: http.StatusOK,
			},
		},
		handlerTestCase{
			name:  "an ungrouped product keeps its own amount",
			setup: seedUngroupedItem,
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/restock/items/item-rice",
				body:           `{"kind":"amount","value":2}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.level", value: "item"},
					{path: "$.target.value", value: float64(2)},
				},
			},
		},
	)
	runHandlerTests(t, tests)
}
