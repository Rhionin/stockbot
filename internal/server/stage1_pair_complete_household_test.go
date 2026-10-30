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

	const beforeName = "AAA Wrong Before"
	const afterName = "ZZZ Wrong After"
	const activeName = "Pat's Pantry"
	const beforeID = "00-g1-decoy-before"
	const afterID = "zz-g1-decoy-after"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, 'auto', datetime('now'), 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 0)`, beforeID, beforeName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE households SET name = ? WHERE is_primary = 1`, beforeName); err != nil {
		t.Fatal(err)
	}

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

	start := c.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start invite %d %s", start.Code, start.Body.String())
	}
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil {
		t.Fatal(err)
	}
	created := c.authed(http.MethodPost, "/api/households", map[string]string{
		"name": activeName, "restockMode": "auto", "inviteCode": startInvite.Code,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create household %d %s", created.Code, created.Body.String())
	}
	var pantry struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &pantry); err != nil || pantry.ID == "" {
		t.Fatal(err)
	}
	switched := c.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": pantry.ID})
	if switched.Code != http.StatusOK {
		t.Fatalf("switch household %d %s", switched.Code, switched.Body.String())
	}
	c.house = pantry.ID

	if afterID <= c.house {
		t.Fatalf("after decoy id must sort after active id %q", c.house)
	}
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, 'auto', datetime('now'), 'zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz', 0)`, afterID, afterName); err != nil {
		t.Fatal(err)
	}

	var lowestRowid int64
	var lowestName string
	if err := db.QueryRow(`SELECT rowid, name FROM households ORDER BY rowid LIMIT 1`).Scan(&lowestRowid, &lowestName); err != nil {
		t.Fatal(err)
	}
	if lowestName != beforeName {
		t.Fatalf("lowest rowid household name must be decoy %q for G1 rowid trap, got %q (rowid %d)", beforeName, lowestName, lowestRowid)
	}
	var highestRowid int64
	var highestName string
	if err := db.QueryRow(`SELECT rowid, name FROM households ORDER BY rowid DESC LIMIT 1`).Scan(&highestRowid, &highestName); err != nil {
		t.Fatal(err)
	}
	if highestName != afterName {
		t.Fatalf("highest rowid household name must be after decoy %q, got %q (rowid %d)", afterName, highestName, highestRowid)
	}
	var activeRowid int64
	if err := db.QueryRow(`SELECT rowid FROM households WHERE id = ?`, c.house).Scan(&activeRowid); err != nil {
		t.Fatal(err)
	}
	if activeRowid == lowestRowid || activeRowid == highestRowid {
		t.Fatalf("active household rowid %d must be strictly between %d and %d (mutant G1)", activeRowid, lowestRowid, highestRowid)
	}
	if lowestName == activeName || highestName == activeName {
		t.Fatal("edge rowid names must differ from active household name")
	}

	var lowestID string
	if err := db.QueryRow(`SELECT id FROM households ORDER BY id LIMIT 1`).Scan(&lowestID); err != nil {
		t.Fatal(err)
	}
	var highestID string
	if err := db.QueryRow(`SELECT id FROM households ORDER BY id DESC LIMIT 1`).Scan(&highestID); err != nil {
		t.Fatal(err)
	}
	if lowestID == c.house || highestID == c.house {
		t.Fatalf("active household %q must not be first or last by id (mutant G1); edges %q .. %q", c.house, lowestID, highestID)
	}
	if lowestID != beforeID {
		t.Fatalf("lowest id must be pre-setup decoy %q, got %q", beforeID, lowestID)
	}
	if highestID != afterID {
		t.Fatalf("highest id must be after decoy %q, got %q", afterID, highestID)
	}

	var onlyRowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM households`).Scan(&onlyRowCount); err != nil {
		t.Fatal(err)
	}
	if onlyRowCount < 4 {
		t.Fatalf("need at least four households for tenant boundary, got %d", onlyRowCount)
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
		t.Fatalf("householdName must not leak edge decoy names: %s", body)
	}
	if !strings.Contains(body, `"householdName":"`+activeName+`"`) {
		t.Fatalf("expected active household name %q in %s", activeName, body)
	}
}
