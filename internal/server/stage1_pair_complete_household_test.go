package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestStage1PairCompleteReturnsHouseholdName(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pair-name.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	if err := svc.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}

	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Pat", "email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d", rec.Code)
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(err)
	}
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	c.house = *session.ActiveHouseholdID

	var householdName string
	if err := db.QueryRow(`SELECT name FROM households WHERE id = ?`, c.house).Scan(&householdName); err != nil {
		t.Fatal(err)
	}

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	complete := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": pairing.Code, "label": "Shelf scanner",
		"hub":  map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-name"},
	}, app.EntryPublic)
	if complete.Code != http.StatusCreated {
		t.Fatalf("pair complete %d %s", complete.Code, complete.Body.String())
	}
	if !strings.Contains(complete.Body.String(), `"householdName":`) ||
		!strings.Contains(complete.Body.String(), `"`+householdName+`"`) {
		t.Fatalf("expected householdName %q in %s", householdName, complete.Body.String())
	}
}

func TestStage1PairCompleteHouseholdNameTenantBoundary(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pair-tenant.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	if err := svc.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}

	const beforeName = "AAA Wrong Before"
	const afterName = "ZZZ Wrong After"
	const beforeID = "00-g1-decoy-before"
	const afterID = "zz-g1-decoy-after"

	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Pat", "email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d", rec.Code)
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(err)
	}
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	c.house = *session.ActiveHouseholdID

	if beforeID >= c.house || afterID <= c.house {
		t.Fatalf("decoy ids must bracket active id %q", c.house)
	}
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, 'auto', datetime('now'), 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 0)`, beforeID, beforeName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, 'auto', datetime('now'), 'zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz', 0)`, afterID, afterName); err != nil {
		t.Fatal(err)
	}

	var activeName string
	if err := db.QueryRow(`SELECT name FROM households WHERE id = ?`, c.house).Scan(&activeName); err != nil {
		t.Fatal(err)
	}
	var firstID string
	if err := db.QueryRow(`SELECT id FROM households ORDER BY id LIMIT 1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if firstID == c.house {
		t.Fatalf("active household %q must not be the first households row (mutant G1)", c.house)
	}
	var onlyRowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM households`).Scan(&onlyRowCount); err != nil {
		t.Fatal(err)
	}
	if onlyRowCount < 3 {
		t.Fatalf("need multiple households for tenant boundary, got %d", onlyRowCount)
	}

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	complete := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": pairing.Code, "label": "Scanner A",
		"hub":  map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-tenant"},
	}, app.EntryPublic)
	if complete.Code != http.StatusCreated {
		t.Fatalf("pair complete %d %s", complete.Code, complete.Body.String())
	}
	body := complete.Body.String()
	if strings.Contains(body, beforeName) || strings.Contains(body, afterName) {
		t.Fatalf("householdName must not leak decoy households: %s", body)
	}
	if !strings.Contains(body, `"householdName":"`+activeName+`"`) {
		t.Fatalf("expected active household name %q in %s", activeName, body)
	}
}
