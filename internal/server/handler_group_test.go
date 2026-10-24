package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestGroupHandlers(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "product cannot join two groups",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Beans")
				mustGroupProduct(env, "p2", "Other")
				insertGroup(env, "group-a", "First")
				insertMember(env, "group-a", "p1")
				insertGroup(env, "group-b", "Second")
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/groups/group-b/members",
				body:           `{"productIds":["p1"]}`,
				expectedStatus: http.StatusConflict,
				assertions:     []assertion{{path: "$.error", value: "That product is already in another group."}},
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodGet,
				path:           "/api/groups/group-a",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.members[0].productId", value: "p1"}},
			}),
		},
		{
			name: "pin that is not a member is rejected",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Beans")
				insertGroup(env, "group-a", "Beans")
				insertMember(env, "group-a", "p1")
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/groups/group-a/rule",
				body:           `{"rule":"favorite","pinnedProductId":"missing","confirm":true}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "member overrides without a target write nothing",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Small")
				mustGroupProduct(env, "p2", "Large")
				mustExec(env, `INSERT INTO items (id, user_id, product_id) VALUES ('item-p1', 'user-1', 'p1'), ('item-p2', 'user-1', 'p2')`)
				mustExec(env, `INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ((SELECT id FROM households WHERE is_primary = 1), 'item-p1', 'amount', 4), ((SELECT id FROM households WHERE is_primary = 1), 'item-p2', 'months', 2)`)
				base, _, err := product.BaseFromAmount(12, "fl oz")
				if err != nil {
					env.T.Fatal(err)
				}
				mustExec(env, `UPDATE products SET net_base_value = ?, net_dimension = 'volume', net_size_origin = 'manual' WHERE id = 'p1'`, base)
				mustExec(env, `UPDATE products SET net_dimension = 'mass' WHERE id = 'p2'`)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/groups",
				body:           `{"name":"Beans","productIds":["p1","p2"]}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "target_decision_required"},
					{path: "$.members[0].productId", value: "p1"},
					{path: "$.members[0].quantity", value: float64(4)},
					{path: "$.members[0].amount", value: float64(48)},
					{path: "$.members[0].dimension", value: "volume"},
					{path: "$.members[1].windowMonths", value: float64(2)},
					{path: "$.members[1].dimension", absent: true},
					{path: "$.members[1].amount", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/groups",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"[]"},
					bodyExcludes:   []string{"Beans"},
				},
				httpExchange{
					method:         http.MethodPost,
					path:           "/api/groups",
					body:           `{"name":"Beans","productIds":["p1","p2"],"target":{"quantity":24}}`,
					expectedStatus: http.StatusCreated,
					assertions: []assertion{
						{path: "$.quantity", value: float64(24)},
						{path: "$.dimension", value: "volume"},
					},
				},
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/products/p1/supply-override",
					expectedStatus: http.StatusConflict,
					bodyContains:   []string{`"code":"in_group"`, `"groupId"`},
				},
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/products/p2/supply-override",
					expectedStatus: http.StatusConflict,
					bodyContains:   []string{`"code":"in_group"`, `"groupId"`},
				},
			),
		},
		{
			name: "an unsized quantity override is a count, not ounces",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Small")
				mustExec(env, `INSERT INTO items (id, user_id, product_id) VALUES ('item-p1', 'user-1', 'p1')`)
				mustExec(env, `INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ((SELECT id FROM households WHERE is_primary = 1), 'item-p1', 'amount', 4)`)
				mustExec(env, `UPDATE products SET net_dimension = 'volume' WHERE id = 'p1'`)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/groups",
				body:           `{"name":"Beans","productIds":["p1"]}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.members[0].quantity", value: float64(4)},
					{path: "$.members[0].dimension", absent: true},
					{path: "$.members[0].amount", absent: true},
				},
			},
		},
		{
			name: "group usage is ounces per month when a rate qualifies",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Powder")
				base, _, err := product.BaseFromAmount(16, "oz")
				if err != nil {
					env.T.Fatal(err)
				}
				mustExec(env, `UPDATE products SET net_base_value = ?, net_dimension = 'mass' WHERE id = 'p1'`, base)
				mustExec(env, `INSERT INTO items (id, user_id, product_id) VALUES ('item-1', 'user-1', 'p1')`)
				insertGroup(env, "group-a", "Powder")
				insertMember(env, "group-a", "p1")
				now := time.Now().UTC()
				started := now.Add(-90 * 24 * time.Hour)
				firstIn := started.Add(time.Second)
				mustExec(env, `INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', ?)`, started.Format(time.RFC3339))
				mustExec(env, `INSERT INTO stock_in_events (id, product_id, at) VALUES (1, 'p1', ?)`, firstIn)
				mustExec(env, `INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('c1', 'item-1', ?)`, firstIn.Add(30*24*time.Hour))
				mustExec(env, `INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('c2', 'item-1', ?)`, firstIn.Add(60*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/groups/group-a",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.usage.perMonth", value: float64(16)},
					{path: "$.usage.unit", value: "oz"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         http.MethodPut,
					path:           "/api/groups/group-a/target",
					body:           `{"quantity":48,"dimension":"mass"}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.quantity", value: float64(48)},
						{path: "$.usage.perMonth", value: float64(16)},
					},
				},
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/groups",
					expectedStatus: http.StatusOK,
					bodyExcludes:   []string{`"usage"`},
				},
			),
		},
		{
			name: "group usage is omitted until a rate qualifies",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Powder")
				mustExec(env, `INSERT INTO items (id, user_id, product_id) VALUES ('item-1', 'user-1', 'p1')`)
				insertGroup(env, "group-a", "Powder")
				insertMember(env, "group-a", "p1")
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/groups/group-a",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.usage", absent: true}},
			},
		},
		{
			name: "dismissed suggestion stays gone",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "A")
				mustGroupProduct(env, "p2", "B")
				mustExec(env, `INSERT INTO group_suggestions (id, user_id, kind, title, status) VALUES ('sug-1', 'user-1', 'looks_alike', 'Beans', 'open')`)
				mustExec(env, `INSERT INTO group_suggestion_members (suggestion_id, product_id, included) VALUES ('sug-1', 'p1', 1), ('sug-1', 'p2', 1)`)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/sug-1/dismiss",
				expectedStatus: http.StatusNoContent,
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				bodyContains:   []string{"[]"},
				bodyExcludes:   []string{"sug-1"},
			}),
		},
		{
			name: "preview sentences",
			httpExchange: httpExchange{
				method: http.MethodPost,
				path:   "/api/groups/preview",
				body: `{
					"rule":"same_as_ran_out",
					"members":[
						{"productId":"hunts","name":"Hunt's 14.5 oz","lastConsumedAt":"2026-01-02T00:00:00Z"},
						{"productId":"gv","name":"Great Value 14.5 oz","lastConsumedAt":"2026-03-02T00:00:00Z"}
					]
				}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.productId", value: "gv"},
					{path: "$.because", value: "The last one used up was Great Value 14.5 oz."},
				},
			},
		},
		{
			name: "preview favorite and best deal",
			httpExchange: httpExchange{
				method: http.MethodPost,
				path:   "/api/groups/preview",
				body: `{
					"rule":"favorite",
					"pinnedProductId":"a",
					"members":[{"productId":"a","name":"Starred"},{"productId":"b","name":"Other"}]
				}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.because", value: "This is the one with the star."}},
			},
			afterRequest: exchanges(httpExchange{
				method: http.MethodPost,
				path:   "/api/groups/preview",
				body: `{
					"rule":"best_deal",
					"members":[{"productId":"gv","name":"Great Value 14.5 oz"}],
					"deals":[{"productId":"gv","priceCents":59,"regularPriceCents":89,"notedAt":"2026-10-05T15:00:00Z"}]
				}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.because", value: "On sale for $0.59, usually $0.89. You noted this sale on Oct 5."}},
			}),
		},
		{
			name: "rename delete remove and target",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Beans")
				mustGroupProduct(env, "p2", "Corn")
				insertGroup(env, "group-a", "Beans")
				insertMember(env, "group-a", "p1")
			},
			httpExchange: httpExchange{
				method:         http.MethodPatch,
				path:           "/api/groups/group-a",
				body:           `{"name":"Pantry beans"}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.name", value: "Pantry beans"}},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         http.MethodPut,
					path:           "/api/groups/group-a/target",
					body:           `{"windowMonths":6}`,
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.windowMonths", value: float64(6)}},
				},
				httpExchange{
					method:         http.MethodPost,
					path:           "/api/groups/group-a/members",
					body:           `{"productIds":["p2"],"target":{"quantity":10}}`,
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.quantity", value: float64(10)}},
				},
				httpExchange{
					method:         http.MethodDelete,
					path:           "/api/groups/group-a/members/p1",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.deleted", value: false}},
				},
				httpExchange{
					method:         http.MethodDelete,
					path:           "/api/groups/group-a",
					expectedStatus: http.StatusNoContent,
				},
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/groups/group-a",
					expectedStatus: http.StatusNotFound,
				},
			),
		},
		{
			name: "accept and skip suggestions",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "A")
				mustGroupProduct(env, "p2", "B")
				mustExec(env, `INSERT INTO group_suggestions (id, user_id, kind, title, proposed_rule, pinned_product_id, status) VALUES ('sug-ok', 'user-1', 'looks_alike', 'Beans', 'favorite', 'p1', 'open')`)
				mustExec(env, `INSERT INTO group_suggestion_members (suggestion_id, product_id, included) VALUES ('sug-ok', 'p1', 1), ('sug-ok', 'p2', 1)`)
				mustExec(env, `INSERT INTO group_suggestions (id, user_id, kind, title, status) VALUES ('sug-skip', 'user-1', 'from_scan', 'Later', 'open')`)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/sug-ok/accept",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.name", value: "Beans"},
					{path: "$.rule", value: "favorite"},
					{path: "$.pinnedProductId", value: "p1"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/sug-skip/skip",
				expectedStatus: http.StatusNoContent,
			}, httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].id", value: "sug-skip"}},
			}),
		},
		{
			name: "preview a saved group",
			setup: func(env testEnv) {
				mustGroupProduct(env, "p1", "Hunt's 14.5 oz")
				insertGroup(env, "group-a", "Beans")
				insertMember(env, "group-a", "p1")
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/groups/preview",
				body:           `{"groupId":"group-a","rule":"same_as_ran_out"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.productId", value: "p1"},
					{path: "$.because", value: "Nothing has run out yet."},
				},
			},
		},
		{
			name: "suggestion members include picture brand size and barcode",
			setup: func(env testEnv) {
				base := 10 * 29.5735295625
				pack := 6
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID:            "relish-olive",
					Name:          "Mt. Olive Sweet Relish, 10 FL OZ",
					Category:      "Condiments",
					UnitOfMeasure: "jar",
					ImageURL:      "https://images.openfoodfacts.org/images/relish.jpg",
					NetBaseValue:  &base,
					NetDimension:  product.DimensionVolume,
					NetSizeOrigin: product.OriginOff,
					PackCount:     &pack,
				}); err != nil {
					env.T.Fatal(err)
				}
				mustGroupProduct(env, "relish-plain", "Sweet Relish")
				mustExec(env, `INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES ('0009300000444', 'relish-olive', 'global', '')`)
				mustExec(env, `INSERT INTO group_suggestions (id, user_id, kind, title, status) VALUES ('sug-relish', 'user-1', 'looks_alike', 'Sweet relish', 'open')`)
				mustExec(env, `INSERT INTO group_suggestion_members (suggestion_id, product_id, included, caution) VALUES ('sug-relish', 'relish-olive', 1, ''), ('sug-relish', 'relish-plain', 1, '')`)
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].title", value: "Sweet relish"},
					{path: "$[0].members[0].productId", value: "relish-olive"},
					{path: "$[0].members[0].brand", value: "Mt. Olive"},
					{path: "$[0].members[0].imageUrl", value: "https://images.openfoodfacts.org/images/relish.jpg"},
					{path: "$[0].members[0].netAmount", value: float64(10)},
					{path: "$[0].members[0].netUnit", value: "fl oz"},
					{path: "$[0].members[0].packCount", value: float64(6)},
					{path: "$[0].members[0].unitOfMeasure", value: "jar"},
					{path: "$[0].members[0].category", value: "Condiments"},
					{path: "$[0].members[0].barcodes[0]", value: "0009300000444"},
					{path: "$[0].members[0].variety", absent: true},
					{path: "$[0].members[1].productId", value: "relish-plain"},
					{path: "$[0].members[1].name", value: "Sweet Relish"},
					{path: "$[0].members[1].brand", absent: true},
					{path: "$[0].members[1].imageUrl", absent: true},
					{path: "$[0].members[1].netAmount", absent: true},
					{path: "$[0].members[1].barcodes", absent: true},
					{path: "$[1]", absent: true},
				},
			},
		},
		{
			name: "rescan finds a national brand group once",
			setup: func(env testEnv) {
				mustGroupProduct(env, "heinz", "Heinz Tomato Ketchup")
				mustGroupProduct(env, "hunts", "Hunt's Tomato Ketchup")
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/rescan",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].title", value: "Tomato ketchup"},
					{path: "$[0].kind", value: "looks_alike"},
					{path: "$[0].members[0].productId", value: "heinz"},
					{path: "$[0].members[1].productId", value: "hunts"},
					{path: "$[1]", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/rescan",
				expectedStatus: http.StatusOK,
				bodyContains:   []string{"[]"},
			}),
		},
		{
			name: "rescan reports no new groups",
			setup: func(env testEnv) {
				mustGroupProduct(env, "apple", "Apple Juice")
				mustGroupProduct(env, "beans", "Black Beans")
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/group-suggestions/rescan",
				expectedStatus: http.StatusOK,
				bodyContains:   []string{"[]"},
			},
		},
		{
			name: "opening suggestions creates one look-alike card",
			setup: func(env testEnv) {
				mustGroupProduct(env, "gv", "Great Value Cut Green Beans")
				mustGroupProduct(env, "kr", "Kroger Cut Green Beans")
				mustGroupProduct(env, "dm", "Del Monte Cut Green Beans")
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].kind", value: "looks_alike"},
					{path: "$[0].title", value: "Cut green beans"},
					{path: "$[0].members[2].productId", value: "kr"},
					{path: "$[1]", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].title", value: "Cut green beans"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "default rule is copied onto a new group",
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/settings/group-rule",
				body:           `{"rule":"favorite"}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.rule", value: "favorite"}},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         http.MethodPost,
					path:           "/api/groups",
					body:           `{"name":"Empty"}`,
					expectedStatus: http.StatusCreated,
					assertions: []assertion{
						{path: "$.rule", value: "favorite"},
						{path: "$.ruleConfirmed", value: false},
					},
				},
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/settings/group-rule",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.rule", value: "favorite"}},
				},
			),
		},
		{
			name: "don't restock stays on the product and out of the rotation",
			setup: func(env testEnv) {
				mustGroupProduct(env, "plain", "Plain muffin")
				mustGroupProduct(env, "fancy", "Fancy muffin")
				insertGroup(env, "mixes", "Muffin mix")
				insertMember(env, "mixes", "plain")
				insertMember(env, "mixes", "fancy")
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				path:           "/api/groups/mixes/members/fancy/restock",
				body:           `{"noRestock":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.members[0].productId", value: "fancy"},
					{path: "$.members[0].noRestock", value: true},
					{path: "$.members[1].productId", value: "plain"},
					{path: "$.members[1].noRestock", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         http.MethodGet,
					path:           "/api/groups/mixes",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.members[0].noRestock", value: true}},
				},
				httpExchange{
					method:         http.MethodPut,
					path:           "/api/groups/mixes/rule",
					body:           `{"rule":"favor_variety","confirm":true}`,
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.rule", value: "favor_variety"}},
				},
				httpExchange{
					method:         http.MethodPut,
					path:           "/api/groups/mixes/members/fancy/restock",
					body:           `{"noRestock":false}`,
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.members[0].noRestock", absent: true}},
				},
			),
		},
		{
			name: "preview skips products that are not restocked",
			httpExchange: httpExchange{
				method: http.MethodPost,
				path:   "/api/groups/preview",
				body: `{
					"rule":"favor_variety",
					"members":[
						{"productId":"fancy","name":"Fancy","noRestock":true,"lastStockedAt":"2024-01-01T00:00:00Z"},
						{"productId":"plain","name":"Plain","lastStockedAt":"2026-01-01T00:00:00Z"},
						{"productId":"new","name":"New"}
					]
				}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.productId", value: "new"},
					{path: "$.because", value: "Next up: New · rotates through 2"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method: http.MethodPost,
					path:   "/api/groups/preview",
					body: `{
						"rule":"same_as_ran_out",
						"members":[
							{"productId":"fancy","name":"Fancy","noRestock":true,"lastConsumedAt":"2026-04-01T00:00:00Z"},
							{"productId":"plain","name":"Plain","lastConsumedAt":"2026-01-01T00:00:00Z"}
						]
					}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.productId", value: "plain"},
						{path: "$.because", value: "Fancy isn't restocked. The last one used up was Plain."},
					},
				},
				httpExchange{
					method: http.MethodPost,
					path:   "/api/groups/preview",
					body: `{
						"rule":"favorite",
						"pinnedProductId":"fancy",
						"members":[
							{"productId":"fancy","name":"Fancy","noRestock":true},
							{"productId":"plain","name":"Plain","lastConsumedAt":"2026-01-01T00:00:00Z"}
						]
					}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.productId", value: "plain"},
						{path: "$.because", value: "Fancy isn't restocked. The last one used up was Plain."},
					},
				},
				httpExchange{
					method: http.MethodPost,
					path:   "/api/groups/preview",
					body: `{
						"rule":"best_deal",
						"pinnedProductId":"fancy",
						"members":[
							{"productId":"fancy","name":"Fancy","noRestock":true},
							{"productId":"plain","name":"Plain"}
						]
					}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.productId", value: "plain"},
						{path: "$.because", value: "Fancy isn't restocked. Nothing is on sale and no fallback is set. Nothing has run out yet."},
					},
				},
				httpExchange{
					method: http.MethodPost,
					path:   "/api/groups/preview",
					body: `{
						"rule":"favor_variety",
						"members":[
							{"productId":"a","name":"A","noRestock":true},
							{"productId":"b","name":"B","noRestock":true}
						]
					}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.productId", value: ""},
						{path: "$.because", value: "Every product in this group is marked don't restock."},
					},
				},
			),
		},
	})
}

func mustGroupProduct(env testEnv, id, name string) {
	env.T.Helper()
	if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{ID: id, Name: name}); err != nil {
		env.T.Fatal(err)
	}
}

func insertGroup(env testEnv, id, name string) {
	env.T.Helper()
	mustExec(env, `INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES (?, 'user-1', ?, lower(?), 'same_as_ran_out')`, id, name, name)
}

func insertMember(env testEnv, groupID, productID string) {
	env.T.Helper()
	mustExec(env, `INSERT INTO product_group_members (product_id, group_id) VALUES (?, ?)`, productID, groupID)
}

func mustExec(env testEnv, query string, args ...any) {
	env.T.Helper()
	if _, err := env.DB.Exec(query, args...); err != nil {
		env.T.Fatal(err)
	}
}
