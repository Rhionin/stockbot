package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestEventsWriteOKBeforeTheFirstEvent(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	handler := &EventsHandler{Broadcaster: broadcaster, Keepalive: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.Handle(rec, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.Body.String(), ": ok\n\n") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(rec.Body.String(), ": ok\n\n") {
		t.Fatalf("body %q", rec.Body.String())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestEleventhStreamSendsRetryAfter(t *testing.T) {
	streams := auth.NewStreams()
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	for range 10 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		if _, err := streams.BindLive(ctx, "lan:192.0.2.20", "house-1", "", "lan"); err != nil {
			t.Fatal(err)
		}
	}
	handler := &EventsHandler{Broadcaster: events.NewBroadcaster(), Streams: streams, Keepalive: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "192.0.2.20:443"
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Kind: "lan", HouseholdID: "house-1"}))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestScannerModeStaysInsideHouseholdAndDevice(t *testing.T) {
	shared := newScannerMode(nil)
	book := newModeBook(shared, "house-1", nil, events.NewBroadcaster())
	ctx := context.Background()
	primary := book.For(ctx, "house-1", "")
	other := book.For(ctx, "house-2", "device-a")
	sibling := book.For(ctx, "house-2", "device-b")
	primary.Set("stock_out")
	if other.Get() != "stock_in" || sibling.Get() != "stock_in" {
		t.Fatalf("other %s sibling %s", other.Get(), sibling.Get())
	}
	other.Set("stock_out")
	if primary.Get() != "stock_out" || sibling.Get() != "stock_in" {
		t.Fatalf("primary %s sibling %s", primary.Get(), sibling.Get())
	}
	if book.For(ctx, "house-1", "default") != shared {
		t.Fatal("primary default left the shared scanner")
	}
}

func TestChangesSinceReportsADeletedShoppingLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, env.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO products (id, name) VALUES ('prod-widget-a', 'Widget A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-widget-a', ?, ?, 'prod-widget-a')`,
		scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	store := shopping.NewStore(env.DB)
	line, err := store.AddManualItem(ctx, scope, "item-widget-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeList)
	if err != nil {
		t.Fatal(err)
	}
	same := httptest.NewRequest(http.MethodGet, "/api/shopping-list?since="+cursor, nil)
	sameRec := httptest.NewRecorder()
	handler.ServeHTTP(sameRec, same)
	if sameRec.Code != http.StatusOK || !strings.Contains(sameRec.Body.String(), `"changed":false`) {
		t.Fatalf("unchanged %d %s", sameRec.Code, sameRec.Body.String())
	}
	if sameRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache %q", sameRec.Header().Get("Cache-Control"))
	}
	if err := store.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	changed := httptest.NewRequest(http.MethodGet, "/api/shopping-list?since="+cursor, nil)
	changedRec := httptest.NewRecorder()
	handler.ServeHTTP(changedRec, changed)
	if changedRec.Code != http.StatusOK || !strings.Contains(changedRec.Body.String(), `"changed":true`) {
		t.Fatalf("deleted %d %s", changedRec.Code, changedRec.Body.String())
	}
	if strings.Contains(changedRec.Body.String(), line.ID) {
		t.Fatalf("deleted line still listed %s", changedRec.Body.String())
	}
	foreign := httptest.NewRequest(http.MethodGet, "/api/shopping-list?since="+changes.Cursor("other-house", changes.ScopeList, 1), nil)
	foreignRec := httptest.NewRecorder()
	handler.ServeHTTP(foreignRec, foreign)
	if foreignRec.Code != http.StatusOK || !strings.Contains(foreignRec.Body.String(), `"changed":true`) {
		t.Fatalf("foreign %d %s", foreignRec.Code, foreignRec.Body.String())
	}
	plain := httptest.NewRequest(http.MethodGet, "/api/shopping-list", nil)
	plainRec := httptest.NewRecorder()
	handler.ServeHTTP(plainRec, plain)
	body, _ := io.ReadAll(plainRec.Body)
	if plainRec.Code != http.StatusOK || len(body) == 0 || body[0] != '[' {
		t.Fatalf("plain list %d %s", plainRec.Code, body)
	}
}

func TestDeletesMoveStockScanAndGroupCursors(t *testing.T) {
	handler, env := setupTestWithDB(t)
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, env.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO products (id, name) VALUES ('prod-widget-b', 'Widget B'), ('prod-widget-c', 'Widget C')`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-widget-b', ?, ?, 'prod-widget-b')`,
		scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	pantry := inventory.NewPantry(env.DB)
	inst, err := pantry.AddInstance(ctx, scope, inventory.ItemInstance{ItemID: "item-widget-b", StockInAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	stockCursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if err := pantry.RemoveInstance(ctx, scope, inst.ID, "manual"); err != nil {
		t.Fatal(err)
	}
	stock := httptest.NewRequest(http.MethodGet, "/api/inventory?since="+stockCursor, nil)
	stockRec := httptest.NewRecorder()
	handler.ServeHTTP(stockRec, stock)
	if stockRec.Code != http.StatusOK || !strings.Contains(stockRec.Body.String(), `"changed":true`) {
		t.Fatalf("stock %d %s", stockRec.Code, stockRec.Body.String())
	}

	queue := scan.NewQueue(env.DB)
	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{Barcode: "9999999", Status: scan.Pending, UnitCount: 1, ScannedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	scanCursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeScans)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := scan.Cancelled
	if err := queue.UpdateScanEntry(ctx, scope, created.ID, nil, nil, nil, nil, &cancelled); err != nil {
		t.Fatal(err)
	}
	scans := httptest.NewRequest(http.MethodGet, "/api/scans?since="+scanCursor, nil)
	scansRec := httptest.NewRecorder()
	handler.ServeHTTP(scansRec, scans)
	if scansRec.Code != http.StatusOK || !strings.Contains(scansRec.Body.String(), `"changed":true`) || strings.Contains(scansRec.Body.String(), created.ID) {
		t.Fatalf("scans %d %s", scansRec.Code, scansRec.Body.String())
	}

	qty := 2.0
	groups := group.NewGroups(env.DB)
	made, err := groups.Create(ctx, scope, "Widget set", []string{"prod-widget-b", "prod-widget-c"}, &group.TargetInput{Quantity: &qty})
	if err != nil {
		t.Fatal(err)
	}
	groupCursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if err := groups.Delete(ctx, scope, made.ID); err != nil {
		t.Fatal(err)
	}
	afterGroup, err := changes.Compare(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock, groupCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !afterGroup.Changed {
		t.Fatal("deleting a group reported no changes")
	}
}

func TestKickHouseholdLeavesTheOtherHouseholdOpen(t *testing.T) {
	streams := auth.NewStreams()
	northCtx, northCancel := context.WithCancel(context.Background())
	southCtx, southCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		northCancel()
		southCancel()
	})
	north, err := streams.BindLive(northCtx, auth.UserStreamKey("user-north"), "house-north", "user-north", "account")
	if err != nil {
		t.Fatal(err)
	}
	south, err := streams.BindLive(southCtx, auth.UserStreamKey("user-north"), "house-south", "user-north", "account")
	if err != nil {
		t.Fatal(err)
	}
	streams.KickHousehold("house-north", "user-north")
	select {
	case <-north.Done():
	case <-time.After(time.Second):
		t.Fatal("north stream stayed open")
	}
	select {
	case <-south.Done():
		t.Fatal("south stream closed")
	default:
	}
}
