package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestLinkSuggestionsMatchTheNoItemCommitAndWriteNothing(t *testing.T) {
	handler, env := setupTestWithDB(t)
	seedLinkCard(env)
	before := domainCounts(t, env.DB)
	beforeScan := scanRow(t, env.DB, "scan-pears")

	body := exchangeJSON(t, handler, http.MethodGet, "/api/scans/scan-pears/link-suggestions", "", http.StatusOK)
	var listed struct {
		Suggestions []scan.LinkSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Suggestions) != 3 ||
		listed.Suggestions[0].ItemID != "item-pears" || listed.Suggestions[0].OnHand != 3 ||
		listed.Suggestions[1].Name != "Unrelated Crackers" ||
		listed.Suggestions[2].Name != "Pear Slices" {
		t.Fatalf("suggestions = %+v", listed.Suggestions)
	}
	for _, suggestion := range listed.Suggestions {
		if suggestion.ItemID == "item-empty" || suggestion.ItemID == "item-shelved" || suggestion.ItemID == "item-twin" {
			t.Fatalf("excluded item was suggested: %+v", listed.Suggestions)
		}
	}
	if !reflect.DeepEqual(domainCounts(t, env.DB), before) {
		t.Fatalf("GET wrote rows: before %v after %v", before, domainCounts(t, env.DB))
	}
	if got := scanRow(t, env.DB, "scan-pears"); got != beforeScan {
		t.Fatalf("scan changed from %s to %s", beforeScan, got)
	}

	commitBody := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-pears/commit", "", http.StatusConflict)
	var commit struct {
		Suggestions []scan.LinkSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(commitBody, &commit); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commit.Suggestions, listed.Suggestions) {
		t.Fatalf("GET %+v, commit %+v", listed.Suggestions, commit.Suggestions)
	}
	pending := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1&status=pending", "", http.StatusOK)
	if !strings.Contains(string(pending), `"id":"scan-pears"`) || !strings.Contains(string(pending), `"productId":"prod-off"`) {
		t.Fatalf("scan no longer pending and unlinked: %s", pending)
	}
}

func TestLinkSuggestionsEmptyList(t *testing.T) {
	handler, env := setupTestWithDB(t)
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-wax", Name: "Xylophone Wax", Category: "Other", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	seedShelfItem(env, "prod-bare", "Xylophone Wax", "item-bare")
	if _, err := env.DB.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('bare-1', 'item-bare', ?)`,
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	// The stock-in trigger unshelves on insert, so the wipe happens after that.
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-bare'`); err != nil {
		t.Fatal(err)
	}
	seedShelfItem(env, "prod-zero", "Xylophone Wax Zero", "item-zero-wax")
	if _, err := env.DB.Exec(`
		INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES ('111', 'prod-zero', 'global', '')`); err != nil {
		t.Fatal(err)
	}
	productID := "prod-wax"
	at := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	seedLooseScan(env, "scan-wax", "000111", scan.StockOut, 1, &productID, scan.Pending, at)

	body := exchangeJSON(t, handler, http.MethodGet, "/api/scans/scan-wax/link-suggestions", "", http.StatusOK)
	if string(body) != `{"suggestions":[]}` && !strings.Contains(string(body), `"suggestions":[]`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(string(body), "null") {
		t.Fatalf("empty list was null: %s", body)
	}
}

func TestLinkSuggestionsErrors(t *testing.T) {
	handler, env := setupTestWithDB(t)
	at := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	productID := "prod-done"
	seedShelfItem(env, productID, "Done", "item-done")
	seedLooseScan(env, "scan-done", "scan-done", scan.StockOut, 1, &productID, scan.Committed, at)
	seedLooseScan(env, "scan-flagged", "scan-flagged", scan.StockOut, 1, &productID, scan.Flagged, at)

	missing := exchangeJSON(t, handler, http.MethodGet, "/api/scans/scan-missing/link-suggestions", "", http.StatusNotFound)
	assertCode(t, missing, "scan_not_found", "This scan is no longer in the queue.")

	handled := exchangeJSON(t, handler, http.MethodGet, "/api/scans/scan-done/link-suggestions", "", http.StatusBadRequest)
	assertCode(t, handled, "not_pending", "This scan was already handled.")

	flagged := exchangeJSON(t, handler, http.MethodGet, "/api/scans/scan-flagged/link-suggestions", "", http.StatusBadRequest)
	assertCode(t, flagged, "not_pending", "This scan was already handled.")
}

func TestLinkSuggestionsHideDatabaseErrors(t *testing.T) {
	const secret = "sqlite: disk I/O error on link candidates"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/scans/{id}/link-suggestions", HandleJSON((&ScanLinkSuggestionsHandler{
		Queue: linkSuggestQueue{err: fmt.Errorf("%s", secret)},
	}).Handle))
	mux.HandleFunc("GET /api/scans/{id}/empty", HandleJSON((&ScanLinkSuggestionsHandler{
		Queue: linkSuggestQueue{},
	}).Handle))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans/scan-1/link-suggestions", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	assertCode(t, rec.Body.Bytes(), "internal", "Something went wrong. Try again.")
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("body leaked the database error: %s", rec.Body.String())
	}

	empty := httptest.NewRecorder()
	mux.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/api/scans/scan-1/empty", nil))
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"suggestions":[]}` {
		t.Fatalf("nil suggestions status %d body %s", empty.Code, empty.Body.String())
	}
}

type linkSuggestQueue struct {
	err error
}

func (q linkSuggestQueue) LinkSuggestions(context.Context, string) ([]scan.LinkSuggestion, error) {
	if q.err != nil {
		return nil, q.err
	}
	return nil, nil
}

func seedLinkCard(env testEnv) {
	env.T.Helper()
	products := []product.Product{
		{ID: "prod-off", Name: "Lite Pear Slices In Pear Juice", Category: "Fruit", UnitOfMeasure: "can"},
		{ID: "prod-pears", Name: "Sliced Pears", Category: "Fruit", UnitOfMeasure: "can"},
		{ID: "prod-crackers", Name: "Unrelated Crackers", Category: "Snacks", UnitOfMeasure: "box"},
		{ID: "prod-slices", Name: "Pear Slices", Category: "Fruit", UnitOfMeasure: "can"},
		{ID: "prod-empty", Name: "Empty Pear", Category: "Fruit", UnitOfMeasure: "can"},
		{ID: "prod-shelved", Name: "Shelved Pear", Category: "Fruit", UnitOfMeasure: "can"},
		{ID: "prod-twin", Name: "Twin Pears", Category: "Fruit", UnitOfMeasure: "can"},
	}
	for _, item := range products {
		if err := env.ProductStore.CreateProduct(context.Background(), item); err != nil {
			env.T.Fatalf("CreateProduct %s: %v", item.ID, err)
		}
	}
	barcodes := []struct{ code, productID, source, userID string }{
		{"011110828385", "prod-off", "global", ""},
		{"828385", "prod-pears", "user_override", "user-1"},
		{"8385", "prod-crackers", "global", ""},
		{"999000111222", "prod-slices", "global", ""},
		{"110828385", "prod-empty", "user_override", "user-1"},
		{"011110828385", "prod-shelved", "user_override", "user-1"},
		{"1110828385", "prod-twin", "user_override", "user-2"},
	}
	for _, barcode := range barcodes {
		if _, err := env.DB.ExecContext(context.Background(),
			`INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES (?, ?, ?, ?)`,
			barcode.code, barcode.productID, barcode.source, barcode.userID); err != nil {
			env.T.Fatalf("barcode %s: %v", barcode.code, err)
		}
	}
	items := []struct {
		id, productID, userID string
		count                 int
	}{
		{"item-pears", "prod-pears", "user-1", 3},
		{"item-crackers", "prod-crackers", "user-1", 4},
		{"item-slices", "prod-slices", "user-1", 2},
		{"item-empty", "prod-empty", "user-1", 0},
		{"item-shelved", "prod-shelved", "user-1", 2},
		{"item-twin", "prod-twin", "user-2", 9},
	}
	now := time.Now()
	for _, item := range items {
		if _, err := env.DB.ExecContext(context.Background(),
			`INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`,
			item.id, item.userID, item.productID); err != nil {
			env.T.Fatalf("item %s: %v", item.id, err)
		}
		for n := 0; n < item.count; n++ {
			if _, err := env.DB.ExecContext(context.Background(),
				`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, ?, ?)`,
				fmt.Sprintf("%s-%d", item.id, n), item.id, now); err != nil {
				env.T.Fatalf("instance %s: %v", item.id, err)
			}
		}
	}
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-shelved'`); err != nil {
		env.T.Fatal(err)
	}
	productID := "prod-off"
	seedLooseScan(env, "scan-pears", "011110828385", scan.StockOut, 1, &productID, scan.Pending, now)
}

func domainCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{
		"products", "items", "item_instances", "barcodes", "scan_entries",
		"stock_moves", "stock_in_events", "consumption_events",
	} {
		counts[table] = mustCount(t, db, `SELECT COUNT(*) FROM `+table)
	}
	return counts
}

func scanRow(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status, productID, committed string
	if err := db.QueryRow(`
		SELECT status, COALESCE(product_id, ''), COALESCE(committed_at, '')
		FROM scan_entries WHERE id = ?`, id).Scan(&status, &productID, &committed); err != nil {
		t.Fatal(err)
	}
	return status + "|" + productID + "|" + committed
}
