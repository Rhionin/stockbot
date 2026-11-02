package server

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seedFixtureStore(t *testing.T, db *sql.DB, householdID, name string) string {
	t.Helper()
	id := uuid.NewString()
	display := strings.TrimSpace(name) + " " + id[:8]
	key := strings.ToLower(display)
	return insertFixtureStore(t, db, householdID, id, display, key)
}

func seedFixtureStoreExact(t *testing.T, db *sql.DB, householdID, display, nameKey string) string {
	t.Helper()
	return insertFixtureStore(t, db, householdID, uuid.NewString(), strings.TrimSpace(display), strings.ToLower(strings.TrimSpace(nameKey)))
}

func insertFixtureStore(t *testing.T, db *sql.DB, householdID, id, display, key string) string {
	t.Helper()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.Exec(`
		INSERT INTO stores (id, household_id, name, name_key, kind, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'physical', 0, ?, ?)`, id, householdID, display, key, when, when)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func checkStoreForeignID(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	storeID := seedFixtureStore(t, fx.DB, fx.Birch.ID, "Birch Market Q7")
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/stores/" + storeID + "/removal-preview", ""},
		{http.MethodDelete, "/api/stores/" + storeID, ""},
		{http.MethodPatch, "/api/stores/" + storeID, `{"name":"Other Q7"}`},
	} {
		body := strings.NewReader(tc.body)
		req := httptest.NewRequest(tc.method, tc.path, body)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		stampHousehold(t, req, fx.Alder)
		rec := httptest.NewRecorder()
		fx.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status %d body %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
	}
}

func checkStoresListScoped(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	seedFixtureStore(t, fx.DB, fx.Birch.ID, "Hidden Birch Q7")
	req := httptest.NewRequest(http.MethodGet, "/api/stores", nil)
	stampHousehold(t, req, fx.Alder)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/stores status %d body %s", rec.Code, raw)
	}
	assertNoLeak(t, raw, fx.Birch)
}

func TestStoresHandlers(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "GET /api/stores empty household",
			httpExchange: httpExchange{
				method:         http.MethodGet,
				url:            "/api/stores",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.stores", value: []any{}},
					{path: "$.lookupsEnabled", value: false},
					{path: "$.canConnect", value: false},
				},
			},
		},
		{
			name: "POST /api/stores creates",
			httpExchange: httpExchange{
				method:         http.MethodPost,
				url:            "/api/stores",
				body:           `{"name":"  Smith's  "}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Smith's"},
					{path: "$.kind", value: "physical"},
					{path: "$.position", value: float64(0)},
				},
			},
		},
		{
			name: "POST duplicate name case-insensitive",
			setup: func(env testEnv) {
				hid := primaryScope(t, env.DB).HouseholdID()
				seedFixtureStoreExact(t, env.DB, hid, "Kroger Q7", "kroger q7")
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				url:            "/api/stores",
				body:           `{"name":"kroger q7"}`,
				expectedStatus: http.StatusConflict,
				bodyContains:   []string{`"code":"store_name_taken"`, `"field":"name"`},
			},
		},
		{
			name: "POST second kroger link rejected",
			setup: func(env testEnv) {
				hid := primaryScope(t, env.DB).HouseholdID()
				when := time.Now().UTC().Format(time.RFC3339Nano)
				_, err := env.DB.Exec(`
					INSERT INTO stores (id, household_id, name, name_key, kind, linked_provider, location_id, location_chain, position, created_at, updated_at)
					VALUES ('linked-a', ?, 'Linked A', 'linked a', 'physical', 'kroger', '12345678', 'KROGER', 0, ?, ?)`, hid, when, when)
				if err != nil {
					t.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				url:            "/api/stores",
				body:           `{"name":"Other Cart","linkedProvider":"kroger","locationId":"87654321","locationChain":"KROGER"}`,
				expectedStatus: http.StatusConflict,
				bodyContains:   []string{`"code":"store_already_linked"`},
			},
		},
		{
			name: "PUT order conflict on bad permutation",
			setup: func(env testEnv) {
				hid := primaryScope(t, env.DB).HouseholdID()
				seedFixtureStore(t, env.DB, hid, "Alpha Q7")
				seedFixtureStore(t, env.DB, hid, "Beta Q7")
			},
			httpExchange: httpExchange{
				method:         http.MethodPut,
				url:            "/api/stores/order",
				body:           `{"storeIds":["missing-id"]}`,
				expectedStatus: http.StatusConflict,
				bodyContains:   []string{`"code":"conflict"`},
			},
		},
	})
}

func TestStoreRemovalPreviewMatchesDelete(t *testing.T) {
	var removeID string
	runHandlerTests(t, []handlerTestCase{{
		name: "preview counts match delete effects",
		setup: func(env testEnv) {
			hid := primaryScope(t, env.DB).HouseholdID()
			id := seedFixtureStore(t, env.DB, hid, "Remove Me Q7")
			removeID = id
			_, err := env.DB.Exec(`INSERT INTO products (id, name) VALUES ('prod-q7', 'Synthetic Q7')`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = env.DB.Exec(`
				INSERT INTO store_trips (id, household_id, product_id, store_id, store_name, store_name_key, kind, occurred_at)
				VALUES ('trip-q7', ?, 'prod-q7', ?, 'Remove Me Q7', 'remove me q7', 'bought', '2026-01-01T00:00:00Z')`, hid, id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = env.DB.Exec(`
				INSERT INTO product_stores (household_id, product_id, store_id, state, is_usual, set_by_user_id, set_at)
				VALUES (?, 'prod-q7', ?, 'carries', 1, 'user-1', '2026-01-01T00:00:00Z')`, hid, id)
				if err != nil {
					t.Fatal(err)
				}
			},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			url:            "/api/stores",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			id := removeID
			if id == "" {
				t.Fatal("missing store id")
			}
			handler := handlerFromEnv(env)
			executeExchange(t, handler, httpExchange{
				method:         http.MethodGet,
				url:            "/api/stores/" + id + "/removal-preview",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.personChoices", value: float64(1)},
					{path: "$.tripsKept", value: float64(1)},
					{path: "$.isDefault", value: false},
				},
			})
			executeExchange(t, handler, httpExchange{
				method:         http.MethodDelete,
				url:            "/api/stores/" + id,
				expectedStatus: http.StatusNoContent,
			})
			var trips int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM store_trips WHERE store_id = ?`, id).Scan(&trips); err != nil || trips != 1 {
				t.Fatalf("trips kept %d err %v", trips, err)
			}
			var stores int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM stores WHERE id = ?`, id).Scan(&stores); err != nil || stores != 0 {
				t.Fatalf("store row left %d", stores)
			}
			var choices int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM product_stores WHERE store_id = ?`, id).Scan(&choices); err != nil || choices != 0 {
				t.Fatalf("product_stores left %d", choices)
			}
		},
	}})
}

func TestStoreOrderAndDefault(t *testing.T) {
	var first, second string
	runHandlerTests(t, []handlerTestCase{{
		name: "order and default",
		setup: func(env testEnv) {
			hid := primaryScope(t, env.DB).HouseholdID()
			first = seedFixtureStore(t, env.DB, hid, "First Q7")
			second = seedFixtureStore(t, env.DB, hid, "Second Q7")
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			url:            "/api/stores",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			executeExchange(t, handler, httpExchange{
				method:         http.MethodPut,
				url:            "/api/stores/order",
				body:           `{"storeIds":["` + second + `","` + first + `"]}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.stores[0].id", value: second},
				},
			})
			executeExchange(t, handler, httpExchange{
				method:         http.MethodPut,
				url:            "/api/stores/default",
				body:           `{"storeId":"` + second + `"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.defaultStoreId", value: second},
				},
			})
		},
	}})
}
