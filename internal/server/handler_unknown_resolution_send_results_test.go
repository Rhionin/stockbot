package server

import (
	"net/http"
	"testing"
	"time"
)

func TestUnknownResolutionUpdatesSendResults(t *testing.T) {
	recentAt := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	runHandlerTests(t, []handlerTestCase{
		{
			name: "reached provider marks unknown line as sent",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-tea", "Tea", "item-tea")
				insertManualShoppingItem(env.T, env.DB, "sli-tea", "user-1", "item-tea", 2)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-tea", "sli-tea", "unknown", "request_incomplete", recentAt, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-tea/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-tea"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[0].reason", absent: true},
					{path: "$.results[0].attemptedAt", value: recentAt},
					{path: "$.results[0].quantity", value: float64(2)},
				},
			}),
		},
		{
			name: "did not reach provider marks unknown line as failed",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-jam", "Jam", "item-jam")
				insertManualShoppingItem(env.T, env.DB, "sli-jam", "user-1", "item-jam", 1)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-jam", "sli-jam", "unknown", "request_incomplete", recentAt, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-jam/unknown-resolution",
				body:           `{"reachedProvider":false,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-jam"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "failed"},
					{path: "$.results[0].reason", value: "request_incomplete"},
					{path: "$.results[0].attemptedAt", value: recentAt},
				},
			}),
		},
		{
			name: "resolution with no stored row still advances ledger",
			setup: func(env testEnv) {
				registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-norow", "Norow", "item-norow")
				insertManualShoppingItem(env.T, env.DB, "sli-norow", "user-1", "item-norow", 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-norow/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list/send-results",
					query:          map[string]string{"itemId": "item-norow"},
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.results", value: []interface{}{}}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/test-none-server_push/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries[0].requested", value: float64(3)}},
				},
			),
		},
		{
			name: "newer unknown attempt for another entry is not clobbered",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-clobber", "Clob", "item-clobber")
				insertManualShoppingItem(env.T, env.DB, "sli-old", "user-1", "item-clobber", 1)
				scope := primaryScope(env.T, env.DB)
				newer := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-clobber", "sli-new", "unknown", "request_incomplete", newer, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-old/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-clobber"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "unknown"},
					{path: "$.results[0].reason", value: "request_incomplete"},
					{path: "$.results[0].entryId", value: "sli-new"},
				},
			}),
		},
		{
			name: "sent row stays sent when owner says it did not reach",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-no-down", "No down", "item-no-down")
				insertManualShoppingItem(env.T, env.DB, "sli-no-down", "user-1", "item-no-down", 2)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-no-down", "sli-no-down", "sent", "", recentAt, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-no-down/unknown-resolution",
				body:           `{"reachedProvider":false,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-no-down"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[0].reason", absent: true},
					{path: "$.results[0].attemptedAt", value: recentAt},
				},
			}),
		},
		{
			name: "failed row stays failed while ledger still advances on reached",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-fail", "Fail", "item-fail")
				insertManualShoppingItem(env.T, env.DB, "sli-fail", "user-1", "item-fail", 3)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-fail", "sli-fail", "failed", "batch_rejected", recentAt, 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-fail/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list/send-results",
					query:          map[string]string{"itemId": "item-fail"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.results[0].status", value: "failed"},
						{path: "$.results[0].reason", value: "batch_rejected"},
						{path: "$.results[0].attemptedAt", value: recentAt},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/test-none-server_push/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries[0].requested", value: float64(3)}},
				},
			),
		},
		{
			name: "not reached write failure returns 500 with row unchanged",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-nrw", "Nrw", "item-nrw")
				insertManualShoppingItem(env.T, env.DB, "sli-nrw", "user-1", "item-nrw", 1)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-nrw", "sli-nrw", "unknown", "request_incomplete", recentAt, 1)
				if _, err := env.DB.Exec(`CREATE TRIGGER block_not_reached_resolve BEFORE UPDATE ON cart_line_results
					WHEN NEW.status = 'failed' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-nrw/unknown-resolution",
				body:           `{"reachedProvider":false,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusInternalServerError,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-nrw"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "unknown"},
					{path: "$.results[0].reason", value: "request_incomplete"},
				},
			}),
		},
		{
			name: "sent row stays sent when resolved again",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-sent", "Sent", "item-sent")
				insertManualShoppingItem(env.T, env.DB, "sli-sent", "user-1", "item-sent", 1)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-sent", "sli-sent", "sent", "", recentAt, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-sent/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-sent"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[0].attemptedAt", value: recentAt},
				},
			}),
		},
		{
			name: "other household send result is unchanged",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				_, err := env.DB.Exec(`
					INSERT INTO households (id, name, created_at, channel_key, is_primary)
					VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`,
					sendResultsCedarHousehold)
				if err != nil {
					env.T.Fatal(err)
				}
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mine", "Mine", "item-mine")
				insertManualShoppingItem(env.T, env.DB, "sli-mine", "user-1", "item-mine", 1)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-mine", "sli-mine", "unknown", "request_incomplete", recentAt, 1)
				insertCartLineResult(env.T, env, sendResultsCedarHousehold, provider, "item-cedar", "sli-cedar", "unknown", "request_incomplete", recentAt, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-mine/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				var status string
				err := env.DB.QueryRow(
					`SELECT status FROM cart_line_results WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
					sendResultsCedarHousehold, "test-none-server_push", "item-cedar").Scan(&status)
				if err != nil || status != "unknown" {
					env.T.Fatalf("cedar row status=%q err=%v", status, err)
				}
			},
		},
		{
			name: "result update failure rolls back ledger advance",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-err", "Err", "item-err")
				insertManualShoppingItem(env.T, env.DB, "sli-err", "user-1", "item-err", 2)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-err", "sli-err", "unknown", "request_incomplete", recentAt, 2)
				if _, err := env.DB.Exec(`CREATE TRIGGER block_unknown_resolve BEFORE UPDATE ON cart_line_results
					WHEN NEW.status = 'sent' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-err/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusInternalServerError,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list/send-results",
					query:          map[string]string{"itemId": "item-err"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.results[0].status", value: "unknown"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/test-none-server_push/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries", value: []any{}}},
				},
			),
		},
	})
}
