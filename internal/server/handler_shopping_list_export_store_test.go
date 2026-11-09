package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
)

const exportStoreProvider = "kroger"

// TestShoppingListExportInPersonStoreRepro asserts mixed-store export behavior through
// the stubbed provider: before #576 every open line reached Add; after the fix the
// in-person line never does.
func TestShoppingListExportInPersonStoreRepro(t *testing.T) {
	var recorder carttest.Calls
	runHandlerTests(t, []handlerTestCase{
		{
			name: "stub receives cart and store-less lines only not in-person",
			setup: func(env testEnv) {
				seedMixedStoreExportFixtures(env, &recorder)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/export",
				body:           `{"provider":"` + exportStoreProvider + `"}`,
				expectedStatus: http.StatusOK,
				bodyContains: []string{
					`"itemId":"item-cart"`,
					`"itemId":"item-loose"`,
				},
				bodyExcludes: []string{
					`"itemId":"item-costco"`,
				},
			},
			afterRequest: func(env testEnv) {
				adds := countCalls(recorder.Calls(), "Add")
				if adds != 2 {
					env.T.Fatalf("Add calls = %d, want 2 (cart + store-less; not in-person)", adds)
				}
				derived := countCalls(recorder.Calls(), "DeriveIdentity")
				if derived != 2 {
					env.T.Fatalf("DeriveIdentity calls = %d, want 2", derived)
				}
			},
		},
		{
			name: "scoped export of in-person line returns 422 and stub sends nothing",
			setup: func(env testEnv) {
				seedMixedStoreExportFixtures(env, &recorder)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/export",
				body:           `{"provider":"` + exportStoreProvider + `","itemIds":["item-costco"]}`,
				expectedStatus: http.StatusUnprocessableEntity,
				assertions: []assertion{
					{path: "$.error", value: "This line is not for your online cart."},
				},
			},
			afterRequest: func(env testEnv) {
				if countCalls(recorder.Calls(), "Add") != 0 {
					env.T.Fatalf("Add calls = %v, want none", recorder.Calls())
				}
			},
		},
		{
			name: "unknown scoped itemIds returns 422",
			setup: func(env testEnv) {
				seedMixedStoreExportFixtures(env, nil)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/export",
				body:           `{"provider":"` + exportStoreProvider + `","itemIds":["item-missing"]}`,
				expectedStatus: http.StatusUnprocessableEntity,
				assertions: []assertion{
					{path: "$.error", value: "That item isn't on your shopping list."},
				},
			},
		},
		{
			name: "GET keeps in-person quantity when cart ledger sent other lines",
			setup: func(env testEnv) {
				seedMixedStoreExportFixtures(env, nil)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/export",
				body:           `{"provider":"` + exportStoreProvider + `"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				scope := primaryScope(env.T, env.DB)
				ledger := cart.NewLedger(env.DB)
				if err := ledger.Advance(context.Background(), scope, cart.ProviderID(exportStoreProvider), "item-costco", 5); err != nil {
					env.T.Fatal(err)
				}
				handler := handlerFromEnv(env)
				raw := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list?provider="+exportStoreProvider, "", http.StatusOK)
				var rows []map[string]any
				if err := json.Unmarshal(raw, &rows); err != nil {
					env.T.Fatal(err)
				}
				byItem := map[string]map[string]any{}
				for _, row := range rows {
					byItem[row["itemId"].(string)] = row
				}
				costco := byItem["item-costco"]
				if _, has := costco["provider"]; has && costco["provider"] != nil && costco["provider"] != "" {
					env.T.Fatalf("in-person provider = %#v, want absent", costco["provider"])
				}
				if costco["quantity"] != float64(2) {
					env.T.Fatalf("in-person quantity = %#v, want 2 (ledger must not apply)", costco["quantity"])
				}
			},
		},
		{
			name: "store-less line sends when only unlinked in-person store exists",
			setup: func(env testEnv) {
				hid := primaryScope(env.T, env.DB).HouseholdID()
				when := time.Now().UTC().Format(time.RFC3339Nano)
				insertInPersonStoreRow(env.T, env.DB, hid, "store-costco-only", when)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-only-inperson", "Snacks", "item-only-inperson")
				insertManualShoppingItem(env.T, env.DB, "sli-only-inperson", "user-1", "item-only-inperson", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "884-snack", "prod-only-inperson", "global", ""); err != nil {
					env.T.Fatal(err)
				}
				registerExportStoreFake(env, &recorder)
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/export",
				body:           `{"provider":"` + exportStoreProvider + `"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				if countCalls(recorder.Calls(), "Add") != 1 {
					env.T.Fatalf("Add calls = %v, want 1 (store-less with sole provider)", recorder.Calls())
				}
			},
		},
	})
}

func countCalls(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

func seedMixedStoreExportFixtures(env testEnv, calls *carttest.Calls) {
	t := env.T
	hid := primaryScope(t, env.DB).HouseholdID()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	onlineStore := insertLinkedStoreRow(t, env.DB, hid, "store-kroger", exportStoreProvider, 0, when)
	costcoStore := insertInPersonStoreRow(t, env.DB, hid, "store-costco", when)

	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-cart", "Online Milk", "item-cart")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-costco", "Bulk Paper", "item-costco")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-loose", "Anything", "item-loose")
	insertShoppingListItemWithRunStore(t, env.DB, "sli-cart", "user-1", "item-cart", 1, onlineStore)
	insertShoppingListItemWithRunStore(t, env.DB, "sli-costco", "user-1", "item-costco", 2, costcoStore)
	insertManualShoppingItem(t, env.DB, "sli-loose", "user-1", "item-loose", 1)

	ctx := context.Background()
	for _, pair := range []struct {
		barcode, prod string
	}{
		{"881-cart", "prod-cart"},
		{"882-loose", "prod-loose"},
		{"883-costco", "prod-costco"},
	} {
		if err := env.ProductStore.UpsertBarcodeMapping(ctx, pair.barcode, pair.prod, "global", ""); err != nil {
			t.Fatalf("barcode: %v", err)
		}
	}
	registerExportStoreFake(env, calls)
}

func registerKrogerForCartDisplay(env testEnv) {
	if env.Registry == nil {
		env.T.Fatal("registry required")
	}
	if _, ok := env.Registry.Get(cart.ProviderID(exportStoreProvider)); ok {
		return
	}
	inner := carttest.NewFake(cart.Capabilities{
		Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly,
		Identity: cart.IdentityDerived,
	}, carttest.NewScript())
	if err := env.Registry.Register(krogerIDProvider{Provider: inner}, cart.WithCredentialsConfigured(true)); err != nil {
		env.T.Fatal(err)
	}
	seedDefaultExportApp(env.T, env.DB, exportStoreProvider)
}

func registerExportStoreFake(env testEnv, calls *carttest.Calls) {
	script := carttest.NewScript().
		WithDispositions(cart.DispositionAccepted, cart.DispositionAccepted).
		WithIdentityLookup("881-cart", cart.ProductIdentity("sku-cart")).
		WithIdentityLookup("882-loose", cart.ProductIdentity("sku-loose")).
		WithIdentityLookup("883-costco", cart.ProductIdentity("sku-costco")).
		WithIdentityLookup("884-snack", cart.ProductIdentity("sku-snack"))
	inner := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthNone,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, script)
	provider := krogerIDProvider{Provider: inner}
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true), cart.WithBatchSize(1)); err != nil {
		env.T.Fatalf("register: %v", err)
	}
	seedDefaultExportApp(env.T, env.DB, exportStoreProvider)
	if calls != nil {
		*calls = inner.(carttest.Calls)
	}
}

type krogerIDProvider struct {
	cart.Provider
}

func (krogerIDProvider) ID() cart.ProviderID { return cart.ProviderID(exportStoreProvider) }

func (k krogerIDProvider) DeriveIdentity(barcode string) (cart.ProductIdentity, bool) {
	if di, ok := k.Provider.(cart.DerivedIdentity); ok {
		return di.DeriveIdentity(barcode)
	}
	return "", false
}

func (k krogerIDProvider) Add(ctx context.Context, cred cart.Credential, req cart.ProvisionRequest) (cart.ProvisionResult, error) {
	if sp, ok := k.Provider.(cart.ServerPush); ok {
		return sp.Add(ctx, cred, req)
	}
	return cart.ProvisionResult{}, nil
}

func insertLinkedStoreRow(t *testing.T, db *sql.DB, householdID, id, provider string, position int, when string) string {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO stores (id, household_id, name, name_key, kind, linked_provider, location_id, location_chain, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'physical', ?, '12345678', 'KROGER', ?, ?, ?)`,
		id, householdID, strings.ToUpper(id), strings.ToLower(id), provider, position, when, when)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertInPersonStoreRow(t *testing.T, db *sql.DB, householdID, id, when string) string {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO stores (id, household_id, name, name_key, kind, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'physical', 1, ?, ?)`,
		id, householdID, strings.ToUpper(id), strings.ToLower(id), when, when)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertShoppingListItemWithRunStore(t *testing.T, db *sql.DB, id, userID, itemID string, quantity int, runStoreID string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, run_store_id) VALUES (?, ?, ?, ?, 'manual', ?)`,
		id, userID, itemID, quantity, runStoreID,
	); err != nil {
		t.Fatalf("insertShoppingListItemWithRunStore: %v", err)
	}
}
