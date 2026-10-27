package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/google/uuid"
)

func TestOperatorHouseholdsAndRenameHousehold(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "operator.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}
	ctx := context.Background()
	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	setup := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Ada Lovelace", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("setup %d %s", setup.Code, setup.Body.String())
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	var opSession auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &opSession); err != nil || !opSession.Operator {
		t.Fatalf("operator session %s", login.Body.String())
	}
	ada := cookieFrom(t, login, site.SessionCookie().Name)
	primary := *opSession.ActiveHouseholdID
	c.cookie = ada
	c.house = primary

	start := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	loftHash, err := auth.HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	loftUserID := uuid.NewString()
	execSQL(t, db, `INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES (?, 'Dee', 'dee@pantry.test', ?, 'local', 0, ?)`,
		loftUserID, loftHash, authStamp(start))
	loftHouse := uuid.NewString()
	execSQL(t, db, `
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Loft', 'ask_hold', ?, 'abcdef0123456789abcdef0123456789ab', 0)`, loftHouse, authStamp(start.Add(time.Hour)))
	execSQL(t, db, `INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		loftHouse, loftUserID, authStamp(start.Add(time.Hour)))

	deletedHouse := uuid.NewString()
	execSQL(t, db, `
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, deleted_at, purge_after)
		VALUES (?, 'Old Pantry', '', ?, 'abcdef0123456789abcdef0123456789ac', 0, ?, ?)`,
		deletedHouse, authStamp(start.Add(2*time.Hour)), authStamp(start.Add(3*24*time.Hour)), authStamp(start.Add(33*24*time.Hour)))
	execSQL(t, db, `INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		deletedHouse, loftUserID, authStamp(start.Add(2*time.Hour)))

	seedHouseholdMetrics(t, db, primary, "prod-a", "item-a", start.Add(4*time.Hour))
	seedHouseholdMetrics(t, db, loftHouse, "prod-b", "item-b", start.Add(5*time.Hour))

	if rec := c.anon(http.MethodGet, "/api/operator/households", nil, app.EntryPublic); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon %d %s", rec.Code, rec.Body.String())
	}

	deeLogin := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "dee@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if deeLogin.Code != http.StatusOK {
		t.Fatalf("dee login %d %s", deeLogin.Code, deeLogin.Body.String())
	}
	deeCookie := cookieFrom(t, deeLogin, site.SessionCookie().Name)
	c.cookie = deeCookie
	c.house = loftHouse
	if rec := c.authed(http.MethodGet, "/api/operator/households", nil); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), auth.CodeForbiddenOperator) {
		t.Fatalf("non-operator %d %s", rec.Code, rec.Body.String())
	}

	c.cookie = ada
	c.house = primary
	rec := c.authed(http.MethodGet, "/api/operator/households", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator list %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Households []auth.OperatorHouseholdSummary
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Households) < 3 {
		t.Fatalf("households %#v", payload.Households)
	}
	byID := map[string]auth.OperatorHouseholdSummary{}
	for _, row := range payload.Households {
		byID[row.ID] = row
	}
	if row := byID[primary]; row.Name == "" || !row.IsPrimary || row.ProductCount != 1 || row.ItemsOnHand != 1 || row.PendingScans != 1 || row.LastActivityAt == "" {
		t.Fatalf("primary metrics %#v", row)
	}
	if row := byID[loftHouse]; row.MemberCount != 1 || row.ProductCount != 1 || len(row.Members) != 1 || row.Members[0].Email != "dee@pantry.test" {
		t.Fatalf("loft %#v", row)
	}
	if row := byID[deletedHouse]; row.DeletedAt == "" || row.PurgeAfter == "" {
		t.Fatalf("deleted %#v", row)
	}

	renamed := c.authed(http.MethodPatch, "/api/households/"+primary, map[string]string{"name": "  North Cupboard  "})
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), "North Cupboard") {
		t.Fatalf("rename trim %d %s", renamed.Code, renamed.Body.String())
	}
	if rec := c.authed(http.MethodPatch, "/api/households/"+primary, map[string]string{"name": "   "}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), auth.CodeInvalidInput) {
		t.Fatalf("blank rename %d %s", rec.Code, rec.Body.String())
	}
	long := strings.Repeat("x", 81)
	if rec := c.authed(http.MethodPatch, "/api/households/"+primary, map[string]string{"name": long}); rec.Code != http.StatusBadRequest {
		t.Fatalf("long rename %d %s", rec.Code, rec.Body.String())
	}

	c.cookie = deeCookie
	c.house = loftHouse
	if rec := c.authed(http.MethodPatch, "/api/households/"+primary, map[string]string{"name": "Hack"}); rec.Code != http.StatusNotFound {
		t.Fatalf("non-member rename %d %s", rec.Code, rec.Body.String())
	}
	c.cookie = ada
	c.house = primary
	invite := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	memberAccept := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Bea", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if memberAccept.Code != http.StatusOK {
		t.Fatalf("accept %d %s", memberAccept.Code, memberAccept.Body.String())
	}
	bea := cookieFrom(t, memberAccept, site.SessionCookie().Name)
	c.cookie = bea
	c.house = primary
	if rec := c.authed(http.MethodPatch, "/api/households/"+primary, map[string]string{"name": "Nope"}); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), auth.CodeForbiddenOwner) {
		t.Fatalf("member rename %d %s", rec.Code, rec.Body.String())
	}
}

func seedHouseholdMetrics(t *testing.T, db *sql.DB, house, productID, itemID string, at time.Time) {
	t.Helper()
	scanID := uuid.NewString()
	moveID := uuid.NewString()
	execSQL(t, db, `INSERT INTO products (id, name) VALUES (?, ?)`, productID, productID)
	execSQL(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`, itemID, house, productID, house)
	execSQL(t, db, `
		INSERT INTO item_instances (id, item_id, household_id, stock_in_at)
		VALUES (?, ?, ?, ?)`, itemID+"-inst", itemID, house, authStamp(at))
	execSQL(t, db, `
		INSERT INTO scan_entries (id, user_id, household_id, barcode, scanned_at, status)
		VALUES (?, ?, ?, '990000000001', ?, 'pending')`, scanID, house, house, authStamp(at.Add(time.Minute)))
	execSQL(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, household_id)
		VALUES (?, ?, ?, 'in', 1, ?, 'manual', ?)`, moveID, itemID, productID, authStamp(at.Add(2*time.Minute)), house)
}

func authStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
