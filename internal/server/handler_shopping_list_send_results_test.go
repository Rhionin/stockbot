package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
)

const sendResultsCedarHousehold = "0190bbbb-cccc-7000-8000-0000000000e5"

func registerSendResultsProvider(t *testing.T, env testEnv) string {
	t.Helper()
	script := carttest.NewScript().WithDispositions(cart.DispositionAccepted)
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthNone,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, script)
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	seedDefaultExportApp(t, env.DB, string(provider.ID()))
	return string(provider.ID())
}

func insertCartLineResult(t *testing.T, env testEnv, household, provider, itemID, entryID, status, reason, attemptedAt string, qty int) {
	t.Helper()
	var reasonArg any
	if reason != "" {
		reasonArg = reason
	}
	var entryArg any
	if entryID != "" {
		entryArg = entryID
	}
	_, err := env.DB.Exec(`
		INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
			entry_id = excluded.entry_id,
			status = excluded.status,
			reason = excluded.reason,
			quantity = excluded.quantity,
			attempted_at = excluded.attempted_at`,
		household, provider, itemID, entryArg, status, reasonArg, qty, attemptedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func TestShoppingListSendResultsRequiresAuth(t *testing.T) {
	db := setupTestDB(t)
	svc := auth.NewService(db, t.TempDir())
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, stage1Site(t)))
	c := &stage1Caller{t: t, h: handler}
	rec := c.anon(http.MethodGet, "/api/shopping-list/send-results", nil, app.EntryPublic)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestShoppingListSendResultsHTTP(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-24 * time.Hour).Format(time.RFC3339)
	expired := now.Add(-cart.SendResultRetention - time.Second).Format(time.RFC3339)

	tests := []handlerTestCase{
		{
			name: "empty household returns empty results",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.retentionDays", value: float64(90)},
					{path: "$.results", value: []interface{}{}},
				},
			},
		},
		{
			name: "unknown provider is rejected",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"provider": "nope"},
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"not registered"},
			},
		},
		{
			name: "more than 200 itemId filters is rejected",
			setup: func(env testEnv) {
				registerSendResultsProvider(env.T, env)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": tooManyItemIDs(201)},
				expectedStatus: http.StatusUnprocessableEntity,
				bodyContains:   []string{"200 items"},
			},
		},
		{
			name: "returns latest visible rows for sole provider",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-a", "e-a", "sent", "", recent, 1)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-b", "", "failed", "batch_rejected", recent, 2)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.retentionDays", value: float64(90)},
					{path: "$.results[0].itemId", value: "item-a"},
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[0].reason", absent: true},
					{path: "$.results[0].entryId", value: "e-a"},
					{path: "$.results[1].itemId", value: "item-b"},
					{path: "$.results[1].status", value: "failed"},
					{path: "$.results[1].reason", value: "batch_rejected"},
					{path: "$.results[1].entryId", absent: true},
				},
			},
		},
		{
			name: "rows older than retention are hidden",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-old", "", "sent", "", expired, 1)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-new", "", "sent", "", recent, 1)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].itemId", value: "item-new"},
					{path: "$.results[1]", absent: true},
				},
			},
		},
		{
			name: "itemId filter returns nothing for another household row",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				_, err := env.DB.Exec(`
					INSERT INTO households (id, name, created_at, channel_key, is_primary)
					VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`,
					sendResultsCedarHousehold)
				if err != nil {
					env.T.Fatal(err)
				}
				insertCartLineResult(env.T, env, sendResultsCedarHousehold, provider, "item-other", "", "sent", "", recent, 1)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				query:          map[string]string{"itemId": "item-other"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results", value: []interface{}{}},
				},
			},
		},
		{
			name: "latest per line keeps one row per item",
			setup: func(env testEnv) {
				provider := registerSendResultsProvider(env.T, env)
				scope := primaryScope(env.T, env.DB)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-z", "e-old", "failed", "api_error", recent, 1)
				newer := now.Add(-time.Hour).Format(time.RFC3339)
				insertCartLineResult(env.T, env, scope.HouseholdID(), provider, "item-z", "e-new", "sent", "", newer, 3)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list/send-results",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.results[0].itemId", value: "item-z"},
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[0].quantity", value: float64(3)},
					{path: "$.results[0].entryId", value: "e-new"},
				},
			},
		},
	}
	runHandlerTests(t, tests)
}

func tooManyItemIDs(n int) string {
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("item-%d", i))
	}
	return strings.Join(ids, ",")
}
