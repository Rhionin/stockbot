package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
)

const oz664 = 28.349523125

type providerHTTPCounter struct{ n int }

func (p *providerHTTPCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	p.n++
	if strings.Contains(req.URL.Path, "/oauth2/token") {
		return provisionTestJSONResponse(http.StatusOK, `{"access_token":"tok","refresh_token":"ref","expires_in":3600}`), nil
	}
	return provisionTestJSONResponse(http.StatusOK, `{}`), nil
}

func TestShoppingListGroupMemberFacts664CrossHouseholdLeak(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name:  "cross-household leak GET and swap",
		setup: setup664CrossHousehold,
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			handler := stagedCartHandler(env)
			getBody, _ := ioReadAllRes(env)
			assert664ContractMember(t, getBody, "item-664-a")
			assert664ContractLeaksAbsent(t, string(getBody))

			var rows []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(getBody, &rows); err != nil || len(rows) != 1 {
				env.T.Fatalf("list rows: %v err %v", rows, err)
			}
			swapBody := exchangeJSON(env.T, handler, http.MethodPost,
				"/api/shopping-list/items/"+rows[0].ID+"/swap",
				`{"itemId":"item-664-partner"}`, http.StatusOK)
			assert664ContractMember(t, swapBody, "item-664-a")
			assert664ContractLeaksAbsent(t, string(swapBody))
		},
	}})
}

func TestShoppingListGroupMemberFacts664ScrubbedImageURL(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "unsafe imageUrl omitted exact sparse JSON",
		setup: func(env testEnv) {
			seed664ProductOnlyItem(env, "prod-664-img", "664 Image Product", "item-664-img")
			mustExec(env, `UPDATE products SET image_url = 'http://127.0.0.1/secret.jpg' WHERE id = 'prod-664-img'`)
			view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 img", []string{"prod-664-img"}, nil)
			if err != nil {
				env.T.Fatal(err)
			}
			beginUsing(env.T, env.DB, time.Now())
			fill664GroupLine(env, "sli-664-img", "item-664-img", view.ID)
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			expectedStatus: http.StatusOK,
			bodyExcludes:   []string{"127.0.0.1", "secret.jpg"},
		},
		afterRequest: func(env testEnv) {
			body, err := ioReadAllRes(env)
			if err != nil {
				env.T.Fatal(err)
			}
			assertMemberJSONExact(env.T, body, "item-664-img",
				`{"itemId":"item-664-img","productId":"prod-664-img","name":"664 Image Product","onHand":0}`)
		},
	}, {
		name: "safe openfoodfacts imageUrl present",
		setup: func(env testEnv) {
			seed664ProductOnlyItem(env, "prod-664-safe-img", "664 Safe Image", "item-664-safe-img")
			mustExec(env, `UPDATE products SET image_url = 'https://images.openfoodfacts.org/thumb.jpg' WHERE id = 'prod-664-safe-img'`)
			view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 safe img", []string{"prod-664-safe-img"}, nil)
			if err != nil {
				env.T.Fatal(err)
			}
			beginUsing(env.T, env.DB, time.Now())
			fill664GroupLine(env, "sli-664-safe", "item-664-safe-img", view.ID)
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			expectedStatus: http.StatusOK,
			bodyContains:   []string{`"imageUrl":"https://images.openfoodfacts.org/thumb.jpg"`},
		},
	}})
}

func TestShoppingListGroupMemberFacts664NoProviderHTTPOrStoreChecksWrite(t *testing.T) {
	counter := &providerHTTPCounter{}
	var beforeCount int
	var beforeRow storeCheckRow664
	runHandlerTests(t, []handlerTestCase{{
		name: "GET and swap leave store_checks and provider HTTP alone",
		setup: func(env testEnv) {
			setup664KrogerSideEffectFixture(env, counter)
			snapshotStoreChecks(env, &beforeCount, &beforeRow)
			fill664GroupFromBeans(env)
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			assertStoreChecksUnchanged(env, beforeCount, beforeRow)
			if counter.n != 0 {
				env.T.Fatalf("provider HTTP calls after GET = %d, want 0", counter.n)
			}
			handler := stagedCartHandler(env)
			var rows []struct{ ID string `json:"id"` }
			body := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
			if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
				env.T.Fatal(err)
			}
			exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/"+rows[0].ID+"/swap",
				`{"itemId":"item-kr"}`, http.StatusOK)
			assertStoreChecksUnchanged(env, beforeCount, beforeRow)
			if counter.n != 0 {
				env.T.Fatalf("provider HTTP calls after swap = %d, want 0", counter.n)
			}
		},
	}})
}

func TestShoppingListGroupMemberFacts664OmittedEmptyFields(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name:  "sparse member optional keys absent",
		setup: setup664OmittedEmptyMembers,
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			body, _ := ioReadAllRes(env)
			assertMemberJSONExact(t, body, "item-664-sparse",
				`{"itemId":"item-664-sparse","productId":"prod-664-sparse","name":"664 Sparse Product","onHand":0}`)
			for _, id := range []string{"prod-664-pack-null", "prod-664-pack-zero"} {
				m := findMemberByProduct(t, body, id)
				if _, ok := m["packCount"]; ok {
					t.Fatalf("%s packCount should be absent: %#v", id, m)
				}
			}
			m2 := findMemberByProduct(t, body, "prod-664-pack-two")
			if m2["packCount"] != float64(2) {
				t.Fatalf("pack two: %#v", m2)
			}
			unk := findMemberByProduct(t, body, "prod-664-net-unknown")
			if _, ok := unk["netAmount"]; ok {
				t.Fatalf("unknown net amount present: %#v", unk)
			}
			if _, ok := unk["netUnit"]; ok {
				t.Fatalf("unknown net unit present: %#v", unk)
			}
			known := findMemberByProduct(t, body, "prod-664-net-known")
			if known["netAmount"] != 14.5 || known["netUnit"] != "oz" {
				t.Fatalf("known net: %#v", known)
			}
			nobc := findMemberByItem(t, body, "item-664-sparse")
			if _, ok := nobc["barcodes"]; ok {
				t.Fatalf("barcodes should be absent: %#v", nobc)
			}
			if strings.Contains(string(body), `"barcodes":[]`) {
				t.Fatal("barcodes empty array present")
			}
		},
	}})
}

func TestShoppingListGroupMemberFacts664PinExistingBehavior(t *testing.T) {
	t.Run("empty list exact []", func(t *testing.T) {
		runHandlerTests(t, []handlerTestCase{{
			name: "empty list when no items",
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$", value: []interface{}{}}},
			},
		}})
	})

	t.Run("not-found swap exact JSON", func(t *testing.T) {
		runHandlerTests(t, []handlerTestCase{{
			name: "missing-line swap",
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/items/missing-line/swap",
				body:           `{"itemId":"item-664-a"}`,
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "shopping list item not found"},
					{path: "$.code", value: "not_found"},
				},
			},
		}})
	})

	t.Run("unauthorized no session Stage1", func(t *testing.T) {
		db := setupTestDB(t)
		svc := auth.NewService(db, t.TempDir())
		handler, _ := setupOnDB(t, db, nil, WithStage1(svc, stage1Site(t)))
		const want = `{"error":"Sign in to continue.","code":"unauthorized"}`
		get := stage1Do(handler, http.MethodGet, "/api/shopping-list", "", nil, app.EntryPublic, "")
		if get.Code != http.StatusUnauthorized || get.Body.String() != want {
			t.Fatalf("GET = %d %s", get.Code, get.Body.String())
		}
		swap := stage1Do(handler, http.MethodPost, "/api/shopping-list/items/line-664/swap", "",
			map[string]string{"itemId": "x"}, app.EntryPublic, "")
		if swap.Code != http.StatusUnauthorized || swap.Body.String() != want {
			t.Fatalf("swap = %d %s", swap.Code, swap.Body.String())
		}
	})

	t.Run("non-member household 404", func(t *testing.T) {
		db := setupTestDB(t)
		setupDir := t.TempDir()
		svc := auth.NewService(db, setupDir)
		site := stage1Site(t)
		handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
		if err := svc.Ensure(t.Context()); err != nil {
			t.Fatal(err)
		}
		code, err := auth.ReadSetupCode(setupDir)
		if err != nil {
			t.Fatal(err)
		}
		setup := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
			"setupCode": code, "name": "Ada", "email": "ada664@test", "password": "correct horse",
		}, app.EntryPublic, "")
		if setup.Code != http.StatusOK {
			t.Fatal(setup.Body.String())
		}
		login := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
			"email": "ada664@test", "password": "correct horse",
		}, app.EntryPublic, "")
		cookie := cookieFrom(t, login, site.SessionCookie().Name)
		foreign := cedarHousehold(t, db)
		for _, spec := range []struct {
			method string
			path   string
			body   any
		}{
			{http.MethodGet, "/api/shopping-list", nil},
			{http.MethodPost, "/api/shopping-list/items/line-664/swap", map[string]string{"itemId": "x"}},
		} {
			raw := stage1Request(spec.method, spec.path, cookie, spec.body, app.EntryPublic, "")
			raw.Header.Set("X-Household-Id", foreign)
			rec2 := httptest.NewRecorder()
			handler.ServeHTTP(rec2, raw)
			const want = `{"error":"Stockbot couldn't find that. It may have been removed.","code":"not_found"}`
			if rec2.Code != http.StatusNotFound || rec2.Body.String() != want {
				t.Fatalf("%s %s = %d %s", spec.method, spec.path, rec2.Code, rec2.Body.String())
			}
		}
	})

	t.Run("foreign household itemId swap 422", func(t *testing.T) {
		runHandlerTests(t, []handlerTestCase{{
			name:  "swap to B item id",
			setup: setup664ForeignItemSwap,
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				list, _ := ioReadAllRes(env)
				var rows []struct {
					ID     string `json:"id"`
					ItemID string `json:"itemId"`
				}
				if err := json.Unmarshal(list, &rows); err != nil || len(rows) != 1 {
					env.T.Fatal(err)
				}
				beforeItem := rows[0].ItemID
				body := exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/"+rows[0].ID+"/swap",
					`{"itemId":"item-664-b"}`, http.StatusUnprocessableEntity)
				const want = `{"error":"Choose a product in this group.","code":"validation"}`
				if string(body) != want {
					env.T.Fatalf("swap body = %s", body)
				}
				after := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
				var again []struct{ ItemID string `json:"itemId"` }
				if err := json.Unmarshal(after, &again); err != nil || len(again) != 1 || again[0].ItemID != beforeItem {
					env.T.Fatalf("item unchanged: %#v", again)
				}
			},
		}})
	})

	t.Run("member order", func(t *testing.T) {
		runHandlerTests(t, []handlerTestCase{{
			name:  "display name then item id",
			setup: setup664MemberOrder,
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				body, _ := ioReadAllRes(env)
				aspen := memberItemIDs(t, body, "664 name order")
				if len(aspen) != 2 || aspen[0] != "item-664-aspen" || aspen[1] != "item-664-cedar" {
					t.Fatalf("name order: %v", aspen)
				}
				same := memberItemIDs(t, body, "664 id order")
				if len(same) != 2 || same[0] != "item-664-m" || same[1] != "item-664-z" {
					t.Fatalf("id order: %v", same)
				}
			},
		}})
	})
}

type storeCheckRow664 struct {
	state                                         string
	started, finished, lease                        sql.NullString
	checked, found                                  int
}

func seed664ProductOnlyItem(env testEnv, productID, name, itemID string) {
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: productID, Name: name, Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		env.T.Fatal(err)
	}
	hid := primaryScope(env.T, env.DB).HouseholdID()
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, 'user-1', ?, ?)`,
		itemID, productID, hid); err != nil {
		env.T.Fatal(err)
	}
}

func setup664CrossHousehold(env testEnv) {
	hidA := primaryScope(env.T, env.DB).HouseholdID()
	hidB := cedarHousehold(env.T, env.DB)
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-664", Name: "664 Contract Product", Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		env.T.Fatal(err)
	}
	seed664HouseholdMember(env, hidA, "user-1", "item-664-a", "Alpha Brand",
		[]string{"111111111111", "999999999999"}, 2, "2026-01-15T12:00:00Z")
	seed664HouseholdMember(env, hidB, "user-2", "item-664-b", "Bravo Brand",
		[]string{"555555555555"}, 7, "2026-06-01T00:00:00Z")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-664-partner", "664 Partner", "item-664-partner")
	view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 contract",
		[]string{"prod-664", "prod-664-partner"}, nil)
	if err != nil {
		env.T.Fatal(err)
	}
	beginUsing(env.T, env.DB, time.Now())
	fill664GroupLine(env, "sli-664-a", "item-664-a", view.ID)
}

func seed664HouseholdMember(env testEnv, householdID, userID, itemID, brand string, barcodes []string, onHand int, stockedAt string) {
	t := env.T
	if userID == "user-2" {
		mustExec(env, `INSERT OR IGNORE INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
			VALUES ('user-2', 'User Two', 'user2-664@test', 'hash-664', 'local', 0, '2026-01-01T00:00:00Z')`)
	}
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, 'prod-664', ?)`,
		itemID, userID, householdID); err != nil {
		t.Fatal(err)
	}
	if brand != "" {
		if _, err := env.DB.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?) ON CONFLICT DO UPDATE SET value = excluded.value`,
			householdID, "product_brand:prod-664", brand); err != nil {
			t.Fatal(err)
		}
	}
	for _, bc := range barcodes {
		if _, err := env.DB.Exec(`INSERT INTO barcodes (household_id, barcode, product_id, source, user_id) VALUES (?, ?, 'prod-664', 'user_override', ?)`,
			householdID, bc, userID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < onHand; i++ {
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("inst-%s-%d", itemID, i), itemID, householdID, stockedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.DB.Exec(`INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, ?, 'prod-664', ?)`,
		householdID, "sin-"+itemID, stockedAt); err != nil {
		t.Fatal(err)
	}
}

func insert664AutoGroupLine(env testEnv, lineID, itemID, groupID string) {
	hid := primaryScope(env.T, env.DB).HouseholdID()
	when := time.Now().UTC().Format(time.RFC3339)
	mustExec(env, `INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at, group_id)
		VALUES (?, ?, ?, 'user-1', 1, 'auto', ?, ?)`, lineID, hid, itemID, when, groupID)
}

func fill664GroupLine(env testEnv, lineID, itemID, groupID string) {
	insert664AutoGroupLine(env, lineID, itemID, groupID)
}

func setup664KrogerSideEffectFixture(env testEnv, counter *providerHTTPCounter) {
	adapter, err := kroger.New("client", "secret", "https://app.test/cb", "PICKUP")
	if err != nil {
		env.T.Fatal(err)
	}
	adapter.WithTransport(counter)
	if env.Registry == nil {
		env.Registry = cart.NewRegistry()
	}
	if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		env.T.Fatal(err)
	}
	hid := primaryScope(env.T, env.DB).HouseholdID()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := env.DB.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri,
			product_access, product_access_at, updated_at
		) VALUES (?, 'user-1', ?, 'kroger', 'client', 'secret', 'https://app.test/cb', 1, ?, ?)`,
		hid+":kroger", hid, when, when); err != nil {
		env.T.Fatal(err)
	}
	if _, err := env.DB.Exec(`
		INSERT INTO provider_connections (
			id, user_id, household_id, provider_id, state, access_token, refresh_token,
			access_token_expires_at, granted_scope, updated_at
		) VALUES ('conn-664', 'user-1', ?, 'kroger', 'connected', 'access-664', 'refresh-664', ?, 'product.compact cart.basic:write', ?)`,
		hid, expires, when); err != nil {
		env.T.Fatal(err)
	}
	storeID := seedFixtureStore(env.T, env.DB, hid, "664 Kroger")
	mustExec(env, `UPDATE stores SET linked_provider = 'kroger', location_id = '01400376', location_chain = 'KROGER' WHERE id = ?`, storeID)
	stale := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	mustExec(env, `
		INSERT INTO store_availability (household_id, store_id, product_id, location_id, status, checked_at, status_expires_at)
		VALUES (?, ?, 'prod-gv', '01400376', 'not_found', ?, ?)`, hid, storeID, stale, past)
	started := when
	if _, err := env.DB.Exec(`
		INSERT INTO store_checks (
			household_id, store_id, state, trigger, started_at, finished_at, lease_until, checked_count, found_count, total_count
		) VALUES (?, ?, 'done', 'manual', ?, ?, ?, 3, 2, 4)`,
		hid, storeID, started, started, future); err != nil {
		env.T.Fatal(err)
	}
}

func fill664GroupFromBeans(env testEnv) {
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv", "Great Value Cut Green Beans", "item-gv")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr", "Kroger Cut Green Beans", "item-kr")
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "111122223333", "prod-gv", "global", ""); err != nil {
		env.T.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO barcodes (household_id, barcode, product_id, source, user_id) VALUES (?, '111122223333', 'prod-gv', 'user_override', 'user-1')`,
		primaryScope(env.T, env.DB).HouseholdID()); err != nil {
		env.T.Fatal(err)
	}
	gv, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 guard group", []string{"prod-gv", "prod-kr"}, nil)
	if err != nil {
		env.T.Fatal(err)
	}
	beginUsing(env.T, env.DB, time.Now())
	insert664AutoGroupLine(env, "sli-664-guard", "item-gv", gv.ID)
}

func snapshotStoreChecks(env testEnv, count *int, row *storeCheckRow664) {
	hid := primaryScope(env.T, env.DB).HouseholdID()
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM store_checks WHERE household_id = ?`, hid).Scan(count); err != nil {
		env.T.Fatal(err)
	}
	if err := env.DB.QueryRow(`
		SELECT state, started_at, finished_at, lease_until, checked_count, found_count
		FROM store_checks WHERE household_id = ? LIMIT 1`, hid).
		Scan(&row.state, &row.started, &row.finished, &row.lease, &row.checked, &row.found); err != nil {
		env.T.Fatal(err)
	}
}

func assertStoreChecksUnchanged(env testEnv, beforeCount int, before storeCheckRow664) {
	var count int
	row := storeCheckRow664{}
	snapshotStoreChecks(env, &count, &row)
	if count != beforeCount {
		env.T.Fatalf("store_checks count = %d, want %d", count, beforeCount)
	}
	if row.state != before.state || row.checked != before.checked || row.found != before.found {
		env.T.Fatalf("store_checks row changed: %+v vs %+v", row, before)
	}
	if row.started != before.started || row.finished != before.finished || row.lease != before.lease {
		env.T.Fatalf("store_checks timestamps changed")
	}
}

func setup664OmittedEmptyMembers(env testEnv) {
	ids := []struct{ prod, item, name string; pack any; net bool }{
		{"prod-664-sparse", "item-664-sparse", "664 Sparse Product", nil, false},
		{"prod-664-pack-null", "item-664-pack-null", "664 Pack Null", nil, false},
		{"prod-664-pack-zero", "item-664-pack-zero", "664 Pack Zero", 0, false},
		{"prod-664-pack-two", "item-664-pack-two", "664 Pack Two", 2, false},
		{"prod-664-net-unknown", "item-664-net-unknown", "664 Net Unknown", nil, false},
		{"prod-664-net-known", "item-664-net-known", "664 Net Known", nil, true},
	}
	var productIDs []string
	for _, row := range ids {
		seed664ProductOnlyItem(env, row.prod, row.name, row.item)
		productIDs = append(productIDs, row.prod)
		if row.pack != nil {
			mustExec(env, `UPDATE products SET pack_count = ? WHERE id = ?`, row.pack, row.prod)
		}
		if row.net {
			mustExec(env, `UPDATE products SET net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual' WHERE id = ?`, 14.5*oz664, row.prod)
		}
	}
	view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 sparse", productIDs, nil)
	if err != nil {
		env.T.Fatal(err)
	}
	beginUsing(env.T, env.DB, time.Now())
	fill664GroupLine(env, "sli-664-sparse", "item-664-sparse", view.ID)
}

func setup664ForeignItemSwap(env testEnv) {
	setup664CrossHousehold(env)
}

func setup664MemberOrder(env testEnv) {
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-aspen", "Aspen 664", "item-664-aspen")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-cedar", "Cedar 664", "item-664-cedar")
	g1, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 name order",
		[]string{"prod-cedar", "prod-aspen"}, nil)
	if err != nil {
		env.T.Fatal(err)
	}
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-same-m", "Same 664", "item-664-m")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-same-z", "Same 664", "item-664-z")
	g2, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "664 id order",
		[]string{"prod-same-z", "prod-same-m"}, nil)
	if err != nil {
		env.T.Fatal(err)
	}
	beginUsing(env.T, env.DB, time.Now())
	fill664GroupLine(env, "sli-664-aspen", "item-664-aspen", g1.ID)
	fill664GroupLine(env, "sli-664-m", "item-664-m", g2.ID)
}

func assert664ContractMember(t *testing.T, body []byte, itemID string) {
	t.Helper()
	want := map[string]any{
		"itemId": itemID, "productId": "prod-664", "name": "664 Contract Product",
		"brand": "Alpha Brand", "onHand": float64(2), "lastStockedAt": "2026-01-15T12:00:00Z",
		"barcodes": []any{"111111111111", "999999999999"},
	}
	got := findMemberByItem(t, body, itemID)
	for k, v := range want {
		if fmt.Sprint(got[k]) != fmt.Sprint(v) {
			t.Fatalf("%s: got %#v want %v", k, got[k], v)
		}
	}
}

func assert664ContractLeaksAbsent(t *testing.T, body string) {
	t.Helper()
	for _, bad := range []string{"Bravo Brand", "555555555555", "2026-06-01"} {
		if strings.Contains(body, bad) {
			t.Fatalf("leaked %q in body", bad)
		}
	}
}

func assertMemberJSONExact(t *testing.T, body []byte, itemID, want string) {
	t.Helper()
	gotBytes, err := json.Marshal(findMemberByItem(t, body, itemID))
	if err != nil {
		t.Fatal(err)
	}
	var gotObj, wantObj any
	if err := json.Unmarshal(gotBytes, &gotObj); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantObj); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotObj) != fmt.Sprint(wantObj) {
		t.Fatalf("member %s = %s want %s", itemID, gotBytes, want)
	}
}

func findMemberByItem(t *testing.T, body []byte, itemID string) map[string]any {
	t.Helper()
	for _, m := range allMembers(t, body) {
		if m["itemId"] == itemID {
			return m
		}
	}
	t.Fatalf("member %s not found", itemID)
	return nil
}

func findMemberByProduct(t *testing.T, body []byte, productID string) map[string]any {
	t.Helper()
	for _, m := range allMembers(t, body) {
		if m["productId"] == productID {
			return m
		}
	}
	t.Fatalf("product %s not found", productID)
	return nil
}

func shoppingListEntries(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	if len(body) > 0 && body[0] == '{' {
		var one map[string]any
		if err := json.Unmarshal(body, &one); err != nil {
			t.Fatal(err)
		}
		return []map[string]any{one}
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func allMembers(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, row := range shoppingListEntries(t, body) {
		group, _ := row["group"].(map[string]any)
		if group == nil {
			continue
		}
		raw, _ := group["members"].([]any)
		for _, entry := range raw {
			m, _ := entry.(map[string]any)
			if m != nil {
				out = append(out, m)
			}
		}
	}
	return out
}

func memberItemIDs(t *testing.T, body []byte, groupName string) []string {
	t.Helper()
	for _, row := range shoppingListEntries(t, body) {
		group, _ := row["group"].(map[string]any)
		if group == nil || group["name"] != groupName {
			continue
		}
		raw, _ := group["members"].([]any)
		ids := make([]string, 0, len(raw))
		for _, entry := range raw {
			m, _ := entry.(map[string]any)
			if m != nil {
				ids = append(ids, m["itemId"].(string))
			}
		}
		return ids
	}
	t.Fatalf("group %q not found", groupName)
	return nil
}

func ioReadAllRes(env testEnv) ([]byte, error) {
	if env.Res == nil {
		env.T.Fatal("missing response")
	}
	return io.ReadAll(env.Res.Body)
}
