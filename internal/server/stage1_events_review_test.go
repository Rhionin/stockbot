package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestLogoutAndSessionDeleteCloseTheEventStream(t *testing.T) {
	handler, _, _ := reviewStack(t)
	tokenA := reviewLogin(t, handler, "alpha@example.test", "correct horse")
	tokenB := reviewLogin(t, handler, "alpha@example.test", "correct horse")
	streamA, doneA := reviewOpenEvents(t, handler, tokenA)
	streamB, doneB := reviewOpenEvents(t, handler, tokenB)
	waitBody(t, streamA, ": ok\n\n")
	waitBody(t, streamB, ": ok\n\n")

	logout := reviewCall(t, handler, http.MethodPost, "/api/auth/logout", tokenA, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout %d %s", logout.Code, logout.Body.String())
	}
	reviewCall(t, handler, http.MethodPost, "/api/scanner/mode", tokenB, map[string]string{"mode": "stock_out"})
	waitDone(t, doneA, "logout left the event stream open")
	if strings.Contains(streamA.Body.String(), "event:") || strings.Contains(streamA.Body.String(), "scanner_mode") || strings.Contains(streamA.Body.String(), "Widget") {
		t.Fatalf("logged-out stream received a later event: %q", streamA.Body.String())
	}
	waitBody(t, streamB, "scanner_mode")

	hashA := auth.HashOpaque(tokenA)
	missing := reviewCall(t, handler, http.MethodDelete, "/api/sessions/"+hashA, tokenB, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("revoked session %d %s", missing.Code, missing.Body.String())
	}
	gone := reviewCall(t, handler, http.MethodDelete, "/api/sessions/"+auth.HashOpaque(tokenB), tokenB, nil)
	if gone.Code != http.StatusNoContent {
		t.Fatalf("delete session %d %s", gone.Code, gone.Body.String())
	}
	reviewCall(t, handler, http.MethodPost, "/api/scanner/mode", tokenB, map[string]string{"mode": "stock_in"})
	waitDone(t, doneB, "session delete left the event stream open")
	if strings.Count(streamB.Body.String(), "scanner_mode") != 1 {
		t.Fatalf("revoked session received a later event: %q", streamB.Body.String())
	}
}

func TestRemovedMemberEventStreamCloses(t *testing.T) {
	handler, svc, db := reviewStack(t)
	owner := reviewLogin(t, handler, "alpha@example.test", "correct horse")
	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(context.Background(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	const member = "user-birch"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Member Birch', 'birch@example.test', ?, 'local', 0, '2026-04-04T00:00:00Z')`, member, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'member', '2026-04-04T00:00:00Z')`, house, member); err != nil {
		t.Fatal(err)
	}
	memberToken := reviewLogin(t, handler, "birch@example.test", "correct horse")
	stream, done := reviewOpenEvents(t, handler, memberToken)
	waitBody(t, stream, ": ok\n\n")
	_ = svc
	removed := reviewCall(t, handler, http.MethodDelete, "/api/households/"+house+"/members/"+member, owner, nil)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove %d %s", removed.Code, removed.Body.String())
	}
	reviewCall(t, handler, http.MethodPost, "/api/scanner/mode", owner, map[string]string{"mode": "stock_out"})
	waitDone(t, done, "removing a member left their event stream open")
	if strings.Contains(stream.Body.String(), "event:") || strings.Contains(stream.Body.String(), "scanner_mode") {
		t.Fatalf("removed member received a later event: %q", stream.Body.String())
	}
}

func TestAccountEventsWithoutAHouseholdAreValidation(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	handler := &EventsHandler{Broadcaster: broadcaster, Keepalive: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Kind: "account", UserID: "user-alpha"}))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.Handle(rec, req)
		close(done)
	}()
	waitDone(t, done, "an account with no household opened a stream")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"validation"`) {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if broadcaster.SubscriberCount() != 0 {
		t.Fatal("validation subscribed to the unscoped broadcaster")
	}

	stack, _, db := reviewStack(t)
	token := reviewLogin(t, stack, "alpha@example.test", "correct horse")
	if _, err := db.Exec(`UPDATE sessions SET active_household_id = '' WHERE id_hash = ?`, auth.HashOpaque(token)); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- reviewCall(t, stack, http.MethodGet, "/api/events", token, nil)
	}()
	select {
	case got := <-finished:
		if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), `"code":"validation"`) {
			t.Fatalf("http %d %s", got.Code, got.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an account with no active household kept the event stream open")
	}
}

func TestAccountStreamsAreCappedPerSession(t *testing.T) {
	streams := auth.NewStreams()
	handler := &EventsHandler{Broadcaster: events.NewBroadcaster(), Streams: streams, Keepalive: time.Hour}
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	open := func(sessionID string) *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		req = req.WithContext(auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: "user-1", SessionID: sessionID, HouseholdID: "house-1"}))
		rec := httptest.NewRecorder()
		go handler.Handle(rec, req)
		return rec
	}
	const firstSession = "sess-a"
	const secondSession = "sess-b"
	for range 10 {
		open(firstSession)
	}
	key := auth.SessionStreamKey(firstSession)
	deadline := time.Now().Add(2 * time.Second)
	for streams.Live(key) < 10 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if streams.Live(key) != 10 {
		t.Fatalf("session bucket has %d streams", streams.Live(key))
	}
	blocked := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	blocked = blocked.WithContext(auth.WithPrincipal(blocked.Context(), auth.Principal{Kind: "account", UserID: "user-1", SessionID: firstSession, HouseholdID: "house-1"}))
	blockedRec := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		handler.Handle(blockedRec, blocked)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("eleventh stream stayed open")
	}
	if blockedRec.Code != http.StatusTooManyRequests || blockedRec.Header().Get("Retry-After") == "" {
		t.Fatalf("eleventh %d %s", blockedRec.Code, blockedRec.Body.String())
	}
	other := auth.SessionStreamKey(secondSession)
	if other == key {
		t.Fatal("sessions shared one stream bucket")
	}
	second := open(secondSession)
	deadline = time.Now().Add(2 * time.Second)
	for streams.Live(other) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if streams.Live(other) != 1 || second.Code == http.StatusTooManyRequests {
		t.Fatalf("second session live %d status %d", streams.Live(other), second.Code)
	}
}

func TestEventStreamReceivesHintFrames(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	const house = "01901111-1111-7000-8000-0000000000a1"
	broadcaster.SetCursor(func(id, scope string) string {
		return changes.Cursor(id, scope, 3)
	})
	handler := &EventsHandler{Broadcaster: broadcaster, Keepalive: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req = req.WithContext(auth.WithPrincipal(ctx, auth.Principal{
		Kind: "account", UserID: "user-alpha", HouseholdID: house, SessionID: "session-hash",
	}))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.Handle(rec, req)
		close(done)
	}()
	waitBody(t, rec, ": ok\n\n")
	broadcaster.PublishHint(house, "stock")
	waitBody(t, rec, `"type":"stock"`)
	broadcaster.PublishHint(house, "devices")
	waitBody(t, rec, `"type":"devices"`)
	body := rec.Body.String()
	stock := sseData(t, body, `"type":"stock"`)
	if len(stock) < 65 || !strings.Contains(stock, `"cursor"`) {
		t.Fatalf("stock hint %q", stock)
	}
	devices := sseData(t, body, `"type":"devices"`)
	if strings.Contains(devices, `"cursor"`) {
		t.Fatalf("devices hint %q", devices)
	}
	cancel()
	waitDone(t, done, "hint stream stayed open")
}

func TestSinceResponsesAreNotStored(t *testing.T) {
	direct := HandleJSON(func(Request[struct{}, struct{}]) (changeView, error) {
		return changeView{Since: "v1.since", Cursor: "v1.now", Changed: true, Key: "items", Items: []string{"Widget A"}}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/inventory?since=v1.since", nil)
	rec := httptest.NewRecorder()
	direct.ServeHTTP(rec, req)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("direct cache %q", rec.Header().Get("Cache-Control"))
	}

	handler, env := setupTestWithDB(t)
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, env.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/inventory", "/api/scans", "/api/shopping-list"} {
		cursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), scopeForPath(path))
		if err != nil {
			t.Fatal(err)
		}
		got := httptest.NewRequest(http.MethodGet, path+"?since="+cursor, nil)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, got)
		if out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %d %q %s", path, out.Code, out.Header().Get("Cache-Control"), out.Body.String())
		}
	}
}

func TestScannerModeRequiresALiveDeviceAndStaysOffOtherHouseholds(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	shared := newScannerMode(func(d scan.ScanDirection) {
		broadcaster.PublishScannerModeFor("house-1", d)
	})
	book := newModeBook(shared, "house-1", nil, broadcaster)
	otherHouse, cancelOther := broadcaster.SubscribeHousehold("house-2", "user-birch", "account")
	defer cancelOther()
	other := book.For(context.Background(), "house-2", "")
	if other == nil {
		t.Fatal("missing default slot")
	}
	other.Set(scan.StockOut)
	select {
	case msg := <-otherHouse:
		t.Fatalf("household 2 received %s %s", msg.EventType, msg.Data)
	case <-time.After(40 * time.Millisecond):
	}
	primary, cancelPrimary := broadcaster.SubscribeHousehold("house-1", "user-alpha", "account")
	defer cancelPrimary()
	shared.Set(scan.StockOut)
	select {
	case msg := <-primary:
		if msg.EventType != "scanner_mode" {
			t.Fatalf("primary event %s", msg.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("primary household missed scanner_mode")
	}

	db := openMigratedDB(t, filepath.Join(t.TempDir(), "modes.db"))
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	live := newModeBook(newScannerMode(nil), scope.HouseholdID(), db, nil)
	for i := range 20 {
		if slot := live.For(ctx, scope.HouseholdID(), "hub-"+strconv.Itoa(i)); slot != nil {
			t.Fatal("unknown device opened a slot")
		}
	}
	if len(live.slots) != 0 {
		t.Fatalf("mode book grew to %d", len(live.slots))
	}
	const counter = "device-counter"
	const pantry = "device-pantry"
	for _, id := range []string{counter, pantry} {
		if _, err = db.Exec(`
			INSERT INTO devices (id, household_id, kind, label, created_at)
			VALUES (?, ?, 'pi_hub', 'Counter', '2026-04-04T00:00:00Z')`, id, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
	}
	left := live.For(ctx, scope.HouseholdID(), counter)
	right := live.For(ctx, scope.HouseholdID(), pantry)
	left.Set(scan.StockOut)
	if right.Get() != scan.StockIn || len(live.slots) != 2 {
		t.Fatalf("slots %d right %s", len(live.slots), right.Get())
	}
	if _, err := db.Exec(`UPDATE devices SET revoked_at = '2026-04-05T00:00:00Z' WHERE id = ?`, counter); err != nil {
		t.Fatal(err)
	}
	if live.For(ctx, scope.HouseholdID(), counter) != nil || len(live.slots) != 1 {
		t.Fatalf("revoked device stayed in the book (%d)", len(live.slots))
	}
}

func scopeForPath(path string) string {
	switch path {
	case "/api/inventory":
		return changes.ScopeStock
	case "/api/scans":
		return changes.ScopeScans
	default:
		return changes.ScopeList
	}
}

func reviewStack(t *testing.T) (http.Handler, *auth.Service, *sql.DB) {
	t.Helper()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, stage1Site(t)))
	if rec := stage1Do(handler, http.MethodGet, "/setup", "", nil, app.EntryPublic, ""); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Owner Alpha", "email": "alpha@example.test", "password": "correct horse",
	}, app.EntryPublic, ""); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	return handler, svc, db
}

func reviewLogin(t *testing.T, handler http.Handler, email, password string) string {
	t.Helper()
	rec := reviewCall(t, handler, http.MethodPost, "/api/auth/login", "", map[string]string{"email": email, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s %d %s", email, rec.Code, rec.Body.String())
	}
	var view auth.SessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || !strings.HasPrefix(view.Token, "sbs_") {
		t.Fatalf("token %s %v", rec.Body.String(), err)
	}
	return view.Token
}

func reviewCall(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := stage1Request(method, path, "", body, app.EntryPublic, "192.0.2.40:443")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Stockbot-Client", "expo/1.2.3")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func reviewOpenEvents(t *testing.T, handler http.Handler, token string) (*httptest.ResponseRecorder, <-chan struct{}) {
	t.Helper()
	req := stage1Request(http.MethodGet, "/api/events", "", nil, app.EntryPublic, "192.0.2.40:443")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Stockbot-Client", "expo/1.2.3")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()
	return rec, done
}

func waitBody(t *testing.T, rec *httptest.ResponseRecorder, needle string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("body missing %q: %q", needle, rec.Body.String())
}

func waitCode(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rec.Code == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status %d, want %d, body %s", rec.Code, want, rec.Body.String())
}

func waitDone(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func sseData(t *testing.T, body, needle string) string {
	t.Helper()
	for _, frame := range strings.Split(body, "\n\n") {
		if strings.Contains(frame, needle) {
			return frame
		}
	}
	t.Fatalf("missing frame %s in %q", needle, body)
	return ""
}
