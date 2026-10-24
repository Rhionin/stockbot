package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestConcurrentBackfillRecordsOnePair(t *testing.T) {
	db := openRaceDB(t)
	handler, env := setupOnDB(t, db, nil)
	t.Cleanup(func() { db.Close() })
	finishOpening(env)
	seedShelfItem(env, "prod-race", "Race oats", "item-race")
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seedScanAt(env, "scan-race", "prod-race", scan.StockOut, 1, at)

	const n = 2
	release := armCommitGate(t, n)
	var done sync.WaitGroup
	codes := make([]int, n)
	bodies := make([]string, n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			codes[i], bodies[i] = postJSON(handler, http.MethodPost, "/api/scans/scan-race/commit", `{"backfill":true}`)
		}(i)
	}
	release()
	done.Wait()

	ok, pending := 0, 0
	for i := range codes {
		switch codes[i] {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest:
			assertCode(t, []byte(bodies[i]), "not_pending", "This scan was already handled.")
			pending++
		default:
			t.Fatalf("response %d = %d %s", i, codes[i], bodies[i])
		}
	}
	if ok != 1 || pending != n-1 {
		t.Fatalf("ok = %d, not_pending = %d, bodies = %v", ok, pending, bodies)
	}
	if nMoves := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-race'`); nMoves != 2 {
		t.Fatalf("moves = %d, want one pair", nMoves)
	}
	if nInst := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-race'`); nInst != 1 {
		t.Fatalf("instances = %d, want 1", nInst)
	}
	var status string
	if err := env.DB.QueryRow(`SELECT status FROM scan_entries WHERE id = 'scan-race'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(scan.Committed) {
		t.Fatalf("status = %s", status)
	}
}

func TestShelvedItemStaysHiddenUntilBackfill(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-live", "Live oats", "item-live")
	seedShelfItem(env, "prod-hidden", "Hidden oats", "item-hidden")
	for _, id := range []string{"hidden-1", "hidden-2"} {
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-hidden', ?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-hidden'`); err != nil {
		t.Fatal(err)
	}
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-other", Name: "Other household", Category: "Pantry", UnitOfMeasure: "unit",
		Source: product.SourceExternal,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, household_id, product_id, shelved) VALUES ('item-other', 'user-2', ?, 'prod-other', 1)`, cedarHousehold(t, env.DB)); err != nil {
		t.Fatal(err)
	}

	ids := productIDs(t, handler, "/api/products")
	if !ids["prod-live"] || ids["prod-hidden"] || !ids["prod-other"] {
		t.Fatalf("product ids = %v", ids)
	}
	other := productIDs(t, handler, "/api/products?userId=user-2")
	if !other["prod-live"] || other["prod-hidden"] || !other["prod-other"] {
		t.Fatalf("userId does not change the product list: %v", other)
	}
	exchangeJSON(t, handler, http.MethodGet, "/api/products/prod-hidden", "", http.StatusOK)

	view := productHistory(env, handler, "item-hidden")
	if view.OnHand != 0 {
		t.Fatalf("shelved on hand = %v", view.OnHand)
	}

	productID := "prod-hidden"
	seedLooseScan(env, "scan-hidden", "066660000000", scan.StockOut, 1, nil, scan.Pending, at)
	before := domainSnapshot(t, env.DB)
	body := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-hidden",
		`{"productId":"`+productID+`","status":"pending"}`, http.StatusConflict)
	assertCode(t, body, "link_target_gone", "That item isn't available anymore. Pick another.")
	if strings.Contains(string(body), `"onHand"`) {
		t.Fatalf("shelved link carried onHand: %s", body)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("shelved link wrote rows:\n%s", got)
	}
}

func TestLinkUsesTheScansHousehold(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-shared", Name: "Shared oats", Category: "Pantry", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-decoy", Name: "Decoy", Category: "Pantry", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	cedar := cedarHousehold(t, env.DB)
	if _, err := env.DB.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-shared-1', 'user-1', (SELECT id FROM households WHERE is_primary = 1), 'prod-shared'),
			('item-shared-2', 'user-2', ?, 'prod-shared'),
			('item-decoy', 'user-2', ?, 'prod-decoy')`, cedar, cedar); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at) VALUES
			('shared-1', 'item-shared-1', ?),
			('shared-2a', 'item-shared-2', ?),
			('shared-2b', 'item-shared-2', ?),
			('shared-2c', 'item-shared-2', ?)`, at, at, at, at); err != nil {
		t.Fatal(err)
	}
	decoy := "prod-decoy"
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
		ID: "scan-shared", UserID: "user-2", Barcode: "077770000000", ScannedAt: at,
		Direction: directionPtr(scan.StockOut), UnitCount: 1, ProductID: &decoy, Status: scan.Pending,
	}); err != nil {
		t.Fatal(err)
	}

	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-shared/commit",
		`{"linkProductId":"prod-shared"}`, http.StatusOK)
	var linked struct {
		OnHand float64 `json:"onHand"`
		Status string  `json:"status"`
	}
	if err := json.Unmarshal(body, &linked); err != nil {
		t.Fatal(err)
	}
	if linked.Status != "committed" || linked.OnHand != 0 {
		t.Fatalf("link commit = %+v", linked)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-shared-1' AND removed_at IS NULL`); n != 0 {
		t.Fatalf("household on hand = %d", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-shared-2' AND removed_at IS NULL`); n != 3 {
		t.Fatalf("other household on hand = %d", n)
	}

	t.Run("patch writes this household's item and barcode", func(t *testing.T) {
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: "prod-patch-2", Name: "Patch two", Category: "Pantry", UnitOfMeasure: "unit",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
			ID: "scan-patch-2", UserID: "user-2", Barcode: "088880000000", ScannedAt: at,
			Direction: directionPtr(scan.StockOut), UnitCount: 1, Status: scan.Pending,
		}); err != nil {
			t.Fatal(err)
		}
		exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-patch-2",
			`{"productId":"prod-patch-2","status":"pending"}`, http.StatusOK)
		var itemUser string
		if err := env.DB.QueryRow(`SELECT user_id FROM items WHERE product_id = 'prod-patch-2'`).Scan(&itemUser); err != nil {
			t.Fatal(err)
		}
		if itemUser != "user-2" {
			t.Fatalf("item user = %s", itemUser)
		}
		var overrideUser string
		if err := env.DB.QueryRow(`
			SELECT user_id FROM barcodes
			WHERE barcode = '088880000000' AND source = 'user_override'`).Scan(&overrideUser); err != nil {
			t.Fatal(err)
		}
		if overrideUser != primaryScope(t, env.DB).HouseholdID() {
			t.Fatalf("override user = %s", overrideUser)
		}
	})

	t.Run("empty shelf names this household's item", func(t *testing.T) {
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: "prod-empty-2", Name: "Empty two", Category: "Pantry", UnitOfMeasure: "unit",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`
			INSERT INTO items (id, user_id, household_id, product_id) VALUES
				('item-empty-1', 'user-1', ?, 'prod-empty-2'),
				('item-empty-2', 'user-2', (SELECT id FROM households WHERE is_primary = 1), 'prod-empty-2')`, cedar); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('empty-1', 'item-empty-1', ?)`, at); err != nil {
			t.Fatal(err)
		}
		if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
			ID: "scan-empty-2", UserID: "user-2", Barcode: "099990000000", ScannedAt: at,
			Direction: directionPtr(scan.StockOut), UnitCount: 1, ProductID: &decoy, Status: scan.Pending,
		}); err != nil {
			t.Fatal(err)
		}
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-empty-2/commit",
			`{"linkProductId":"prod-empty-2"}`, http.StatusConflict)
		var got struct {
			Code   string  `json:"code"`
			ItemID string  `json:"itemId"`
			OnHand float64 `json:"onHand"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Code != "none_on_hand" || got.ItemID != "item-empty-2" || got.OnHand != 0 {
			t.Fatalf("empty shelf = %+v", got)
		}
		var overrideUser string
		if err := env.DB.QueryRow(`
			SELECT user_id FROM barcodes
			WHERE barcode = '099990000000' AND source = 'user_override' AND product_id = 'prod-empty-2'`).Scan(&overrideUser); err != nil {
			t.Fatal(err)
		}
		if overrideUser != primaryScope(t, env.DB).HouseholdID() {
			t.Fatalf("override user = %s", overrideUser)
		}
	})
}

func TestPatchRejectsAMissingProductAndACancelTogether(t *testing.T) {
	handler, env := setupTestWithDB(t)
	at := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-keep", "Keep", "item-keep")
	seedLooseScan(env, "scan-unknown", "011110000077", scan.StockOut, 1, nil, scan.Pending, at)
	before := domainSnapshot(t, env.DB)
	unknown := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-unknown",
		`{"productId":"prod-missing","status":"pending"}`, http.StatusConflict)
	assertCode(t, unknown, "link_target_gone", "That item isn't available anymore. Pick another.")
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("unknown product wrote rows:\n%s", got)
	}

	seedLooseScan(env, "scan-both", "011110000066", scan.StockOut, 1, nil, scan.Pending, at)
	before = domainSnapshot(t, env.DB)
	both := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-both",
		`{"productId":"prod-keep","status":"cancelled"}`, http.StatusBadRequest)
	assertCode(t, both, inventory.CodeConflictingFields, inventory.ConflictingFieldsMessage)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("conflicting patch wrote rows:\n%s", got)
	}

	missing := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-absent",
		`{"productId":"prod-keep","status":"cancelled"}`, http.StatusNotFound)
	if strings.Contains(string(missing), inventory.CodeConflictingFields) {
		t.Fatalf("missing scan = %s", missing)
	}

	for _, status := range []string{"committed", "flagged"} {
		id := "scan-" + status
		seedLooseScan(env, id, "0111100000"+status[:2], scan.StockOut, 1, nil, scan.Pending, at)
		before = domainSnapshot(t, env.DB)
		body := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/"+id,
			`{"productId":"prod-keep","status":"`+status+`"}`, http.StatusBadRequest)
		assertCode(t, body, inventory.CodeConflictingFields, inventory.ConflictingFieldsMessage)
		if got := domainSnapshot(t, env.DB); got != before {
			t.Fatalf("status %s wrote rows:\n%s", status, got)
		}
		var still string
		if err := env.DB.QueryRow(`SELECT status FROM scan_entries WHERE id = ?`, id).Scan(&still); err != nil {
			t.Fatal(err)
		}
		if still != string(scan.Pending) {
			t.Fatalf("status %s left the scan %s", status, still)
		}
	}
}

func TestBackfillQuantityStaysLocked(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-lock", "Locked", "item-lock")
	at := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	seedScanAt(env, "scan-lock", "prod-lock", scan.StockOut, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-lock/commit", `{"backfill":true}`, http.StatusOK)
	view := productHistory(env, handler, "item-lock")
	var inID, outID string
	for _, move := range view.Moves {
		if move.Direction == "in" && move.Source == "backfill" {
			inID = move.ID
		}
		if move.Direction == "out" && move.Source == "scan" {
			outID = move.ID
		}
	}
	if inID == "" || outID == "" {
		t.Fatalf("moves = %+v", view.Moves)
	}

	before := domainSnapshot(t, env.DB)
	inBody := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+inID, `{"quantity":2}`, http.StatusBadRequest)
	assertCode(t, inBody, inventory.CodeBackfillLocked, inventory.BackfillLockedMessage)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("stock-in edit wrote rows:\n%s", got)
	}
	outBody := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+outID, `{"quantity":2}`, http.StatusBadRequest)
	assertCode(t, outBody, inventory.CodeBackfillLocked, inventory.BackfillLockedMessage)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("stock-out edit wrote rows:\n%s", got)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+inID, `{"quantity":1}`, http.StatusOK)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("same quantity wrote rows:\n%s", got)
	}

	seedShelfItem(env, "prod-edit", "Editable", "item-edit")
	seedScanAt(env, "scan-edit", "prod-edit", scan.StockIn, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-edit/commit", "", http.StatusOK)
	edited := productHistory(env, handler, "item-edit")
	moveID := ""
	for _, move := range edited.Moves {
		if move.Direction == "in" {
			moveID = move.ID
		}
	}
	if moveID == "" {
		t.Fatalf("stock-in moves = %+v", edited.Moves)
	}
	body := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+moveID, `{"quantity":2}`, http.StatusOK)
	var after historyView
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if after.OnHand != 2 || moveQty(after.Moves, moveID) != 2 {
		t.Fatalf("edited history = %+v", after)
	}
}

func productIDs(t *testing.T, handler http.Handler, path string) map[string]bool {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, path, "", http.StatusOK)
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("products %s: %v", body, err)
	}
	ids := map[string]bool{}
	for _, row := range rows {
		ids[row.ID] = true
	}
	return ids
}

func openRaceDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "pantry.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := app.OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	if err := app.RunMigrations(db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func TestConcurrentSingleAndBatchCommit(t *testing.T) {
	db := openRaceDB(t)
	handler, env := setupOnDB(t, db, nil)
	t.Cleanup(func() { db.Close() })
	finishOpening(env)
	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	t.Run("stock in", func(t *testing.T) {
		seedShelfItem(env, "prod-race-in", "Race in", "item-race-in")
		seedScanAt(env, "scan-race-in", "prod-race-in", scan.StockIn, 1, at)
		codes, bodies := overlapCommits(t, handler, 2, http.MethodPost, "/api/scans/scan-race-in/commit", "")
		ok, pending := countCommitResults(t, codes, bodies)
		if ok != 1 || pending != 1 {
			t.Fatalf("ok = %d, not_pending = %d, bodies = %v", ok, pending, bodies)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-race-in'`); n != 1 {
			t.Fatalf("instances = %d, want 1", n)
		}
	})

	t.Run("stock out", func(t *testing.T) {
		seedShelfItem(env, "prod-race-out", "Race out", "item-race-out")
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('race-out-1', 'item-race-out', ?)`, at); err != nil {
			t.Fatal(err)
		}
		seedScanAt(env, "scan-race-out", "prod-race-out", scan.StockOut, 1, at)
		codes, bodies := overlapCommits(t, handler, 2, http.MethodPost, "/api/scans/scan-race-out/commit", "")
		ok, pending := countCommitResults(t, codes, bodies)
		if ok != 1 || pending != 1 {
			t.Fatalf("ok = %d, not_pending = %d, bodies = %v", ok, pending, bodies)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-race-out' AND removed_at IS NULL`); n != 0 {
			t.Fatalf("on hand = %d, want 0", n)
		}
	})

	t.Run("batch", func(t *testing.T) {
		seedShelfItem(env, "prod-race-batch", "Race batch", "item-race-batch")
		seedScanAt(env, "scan-race-batch", "prod-race-batch", scan.StockIn, 1, at)
		_, bodies := overlapCommits(t, handler, 2, http.MethodPost, "/api/scans/batch-commit",
			`{"scanEntryIds":["scan-race-batch"],"commit":true}`)
		committed, failed := 0, 0
		for _, body := range bodies {
			var got struct {
				CommittedIDs []string `json:"committedIds"`
				FailedCodes  []string `json:"failedCodes"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("batch %s: %v", body, err)
			}
			if len(got.CommittedIDs) == 1 && got.CommittedIDs[0] == "scan-race-batch" && len(got.FailedCodes) == 0 {
				committed++
			}
			if len(got.FailedCodes) == 1 && got.FailedCodes[0] == "not_pending" && len(got.CommittedIDs) == 0 {
				failed++
			}
		}
		if committed != 1 || failed != 1 {
			t.Fatalf("batch committed = %d failed = %d bodies = %v", committed, failed, bodies)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-race-batch'`); n != 1 {
			t.Fatalf("instances = %d, want 1", n)
		}
	})
}

func overlapCommits(t *testing.T, handler http.Handler, n int, method, path, payload string) ([]int, []string) {
	t.Helper()
	release := armCommitGate(t, n)
	var done sync.WaitGroup
	codes := make([]int, n)
	bodies := make([]string, n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			codes[i], bodies[i] = postJSON(handler, method, path, payload)
		}(i)
	}
	release()
	done.Wait()
	return codes, bodies
}

func countCommitResults(t *testing.T, codes []int, bodies []string) (ok, pending int) {
	t.Helper()
	for i := range codes {
		switch codes[i] {
		case http.StatusOK:
			ok++
		case http.StatusBadRequest:
			assertCode(t, []byte(bodies[i]), "not_pending", "This scan was already handled.")
			pending++
		default:
			t.Fatalf("response %d = %d %s", i, codes[i], bodies[i])
		}
	}
	return ok, pending
}

// armCommitGate holds each request after the handler accepted it and before
// the commit transaction, then lets them all proceed together.
func armCommitGate(t *testing.T, n int) func() {
	t.Helper()
	arrived := make(chan struct{}, n)
	release := make(chan struct{})
	var once sync.Once
	commitAfterCheck = func() {
		arrived <- struct{}{}
		<-release
	}
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		commitAfterCheck = nil
	})
	return func() {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for i := 0; i < n; i++ {
			select {
			case <-arrived:
			case <-deadline:
				t.Fatal("commit did not reach the post-check gate")
			}
		}
		once.Do(func() { close(release) })
	}
}

func postJSON(handler http.Handler, method, path, payload string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(body)
}
