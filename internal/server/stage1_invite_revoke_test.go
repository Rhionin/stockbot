package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestMemberRevokesJoinInviteAndOperatorRevokesStart(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}
	if err := svc.Ensure(context.Background()); err != nil {
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
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.User == nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	ada := cookieFrom(t, login, site.SessionCookie().Name)
	adaID := session.User.ID
	house := *session.ActiveHouseholdID
	c.cookie = ada
	c.house = house

	for _, body := range []any{
		map[string]string{"role": "member"},
		map[string]string{"role": "owner"},
	} {
		rec := c.authed(http.MethodPost, "/api/operator/household-invites", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"validation"`) || !strings.Contains(rec.Body.String(), `"field":"role"`) {
			t.Fatalf("role body %v: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := c.authed(http.MethodPost, "/api/operator/household-invites", map[string]string{"nope": "1"}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"validation"`) || strings.Contains(rec.Body.String(), `"field"`) {
		t.Fatalf("unknown field %d %s", rec.Code, rec.Body.String())
	}
	badJSON := c.callRaw(http.MethodPost, "/api/operator/household-invites", "{", app.EntryPublic, map[string]string{
		"Cookie": site.SessionCookie().Name + "=" + ada, "Origin": "https://pantry.test",
	})
	if badJSON.Code != http.StatusBadRequest || !strings.Contains(badJSON.Body.String(), `"code":"validation"`) {
		t.Fatalf("invalid json %d %s", badJSON.Code, badJSON.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/operator/household-invites", map[string]any{}); rec.Code != http.StatusCreated {
		t.Fatalf("empty object %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/operator/household-invites", nil); rec.Code != http.StatusCreated {
		t.Fatalf("empty body %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/operator/household-invites", map[string]string{"email": "ignored@pantry.test"}); rec.Code != http.StatusCreated {
		t.Fatalf("email ignored %d %s", rec.Code, rec.Body.String())
	}

	first := c.authed(http.MethodPost, "/api/invites", nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("join invite %d %s", first.Code, first.Body.String())
	}
	var join auth.InviteCreated
	if err := json.Unmarshal(first.Body.Bytes(), &join); err != nil {
		t.Fatal(err)
	}
	accepted := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": join.Code, "account": map[string]string{"name": "Bea", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"role":"member"`) {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	bea := cookieFrom(t, accepted, site.SessionCookie().Name)
	var beaSession struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &beaSession); err != nil || beaSession.User.ID == "" {
		t.Fatalf("bea %s", accepted.Body.String())
	}

	open := mustCreateInvite(t, c)
	c.cookie = bea
	revoked := c.authed(http.MethodPost, "/api/invites/"+open.ID+"/revoke", nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("member revoke %d %s", revoked.Code, revoked.Body.String())
	}

	bySession := mustCreateInvite(t, &stage1Caller{t: t, h: handler, cookie: ada, house: house})
	c.house = ""
	if rec := c.authed(http.MethodPost, "/api/invites/"+bySession.ID+"/revoke", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("session household %d %s", rec.Code, rec.Body.String())
	}

	if _, err := db.Exec(`UPDATE sessions SET active_household_id = '' WHERE user_id = ?`, beaSession.User.ID); err != nil {
		t.Fatal(err)
	}
	missing := mustCreateInvite(t, &stage1Caller{t: t, h: handler, cookie: ada, house: house})
	c.cookie = bea
	c.house = ""
	if rec := c.authed(http.MethodPost, "/api/invites/"+missing.ID+"/revoke", nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"validation"`) || !strings.Contains(rec.Body.String(), `"field":"householdId"`) {
		t.Fatalf("missing household %d %s", rec.Code, rec.Body.String())
	}
	other := "0190aaaa-aaaa-7000-8000-000000000099"
	if rec := c.call(http.MethodPost, "/api/invites/"+missing.ID+"/revoke", nil, app.EntryPublic, map[string]string{"X-Household-Id": other}); rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched household %d %s", rec.Code, rec.Body.String())
	}

	if _, err := db.Exec(`UPDATE sessions SET active_household_id = '' WHERE user_id = ?`, adaID); err != nil {
		t.Fatal(err)
	}
	c.cookie = ada
	c.house = ""
	start := c.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start invite %d %s", start.Code, start.Body.String())
	}
	var created auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("start %s", start.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/invites/"+created.ID+"/revoke", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("operator revoke without household %d %s", rec.Code, rec.Body.String())
	}
	foreign := c.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if foreign.Code != http.StatusCreated {
		t.Fatalf("second start %d %s", foreign.Code, foreign.Body.String())
	}
	var ignored auth.InviteCreated
	if err := json.Unmarshal(foreign.Body.Bytes(), &ignored); err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/api/invites/"+ignored.ID+"/revoke", nil, app.EntryPublic, map[string]string{"X-Household-Id": other}); rec.Code != http.StatusNoContent {
		t.Fatalf("operator revoke ignores household %d %s", rec.Code, rec.Body.String())
	}
}

func mustCreateInvite(t *testing.T, c *stage1Caller) auth.InviteCreated {
	t.Helper()
	rec := c.authed(http.MethodPost, "/api/invites", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite %d %s", rec.Code, rec.Body.String())
	}
	var created auth.InviteCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("invite %s", rec.Body.String())
	}
	return created
}
