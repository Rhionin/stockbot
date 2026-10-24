package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func TestStage1AuthFlow(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	cookieName := site.SessionCookie().Name

	page := stage1Do(handler, http.MethodGet, "/setup", "", nil, app.EntryLAN, "")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /setup = %d %s", page.Code, page.Body.String())
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	publicSetup := stage1Do(handler, http.MethodGet, "/setup", "", nil, app.EntryPublic, "")
	if publicSetup.Code != http.StatusNotFound {
		t.Fatalf("public GET /setup = %d", publicSetup.Code)
	}
	created := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Ada Lovelace", "email": "Ada@Pantry.Test", "password": "correct horse",
	}, app.EntryLAN, "")
	if created.Code != http.StatusOK {
		t.Fatalf("POST /setup = %d %s", created.Code, created.Body.String())
	}

	login := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Kind != "account" || session.User == nil || session.User.Email != "ada@pantry.test" || !session.Operator {
		t.Fatalf("session = %+v", session)
	}
	if session.Token != "" {
		t.Fatal("web login included a bearer token")
	}
	if session.ActiveHouseholdID == nil || *session.ActiveHouseholdID == "" {
		t.Fatal("login did not store an active household")
	}
	rawCookie := cookieFrom(t, login, cookieName)
	household := *session.ActiveHouseholdID

	authed := stage1Do(handler, http.MethodGet, "/api/auth/session", rawCookie, nil, app.EntryPublic, "")
	if authed.Code != http.StatusOK || !strings.Contains(authed.Body.String(), `"kind":"account"`) {
		t.Fatalf("session = %d %s", authed.Code, authed.Body.String())
	}
	noOrigin := stage1Do(handler, http.MethodPost, "/api/auth/logout", rawCookie, nil, app.EntryPublic, "omit-origin")
	if noOrigin.Code != http.StatusUnauthorized || !strings.Contains(noOrigin.Body.String(), auth.SentenceUnauthorized) {
		t.Fatalf("csrf = %d %s", noOrigin.Code, noOrigin.Body.String())
	}
	foreignReq := stage1Request(http.MethodGet, "/api/household/settings", rawCookie, nil, app.EntryPublic, "")
	foreignReq.Header.Set("X-Household-Id", "0190aaaa-aaaa-7000-8000-000000000099")
	foreignRec := httptest.NewRecorder()
	handler.ServeHTTP(foreignRec, foreignReq)
	if foreignRec.Code != http.StatusNotFound || strings.Contains(foreignRec.Body.String(), "forbidden") {
		t.Fatalf("foreign household = %d %s", foreignRec.Code, foreignRec.Body.String())
	}
	settings := stage1Do(handler, http.MethodGet, "/api/household/settings", rawCookie, nil, app.EntryPublic, "")
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"restockMode"`) {
		t.Fatalf("settings = %d %s", settings.Code, settings.Body.String())
	}

	me := stage1Do(handler, http.MethodDelete, "/api/me", rawCookie, map[string]string{"confirmation": "ada@pantry.test"}, app.EntryPublic, "")
	if me.Code != http.StatusConflict || !strings.Contains(me.Body.String(), auth.LastOwnerSentence("Primary")) {
		t.Fatalf("last owner = %d %s", me.Code, me.Body.String())
	}

	invite := stage1Do(handler, http.MethodPost, "/api/invites", rawCookie, map[string]string{"role": "member"}, app.EntryPublic, "")
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", invite.Code, invite.Body.String())
	}
	var createdInvite auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &createdInvite); err != nil || createdInvite.Code == "" {
		t.Fatalf("invite body %s err %v", invite.Body.String(), err)
	}
	preview := stage1Do(handler, http.MethodPost, "/api/invites/preview", "", map[string]string{"code": createdInvite.Code}, app.EntryPublic, "")
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"join_household"`) {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}

	pair := stage1Do(handler, http.MethodPost, "/api/devices/pair", rawCookie, map[string]any{}, app.EntryPublic, "")
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair = %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	done := stage1Do(handler, http.MethodPost, "/api/devices/pair/complete", "", map[string]any{
		"code": pairing.Code, "hub": map[string]string{"kind": "pi_hub", "version": "1.0.0", "bootId": "boot"},
	}, app.EntryLAN, "")
	if done.Code != http.StatusCreated || !strings.Contains(done.Body.String(), `"heartbeatSeconds":60`) {
		t.Fatalf("pair complete = %d %s", done.Code, done.Body.String())
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &paired); err != nil || !strings.HasPrefix(paired.Token, "sbd_") {
		t.Fatalf("token %s err %v", done.Body.String(), err)
	}
	if strings.Contains(done.Body.String(), household) {
		t.Fatal("pairing response included the household id")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("shared-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key) VALUES (?, 'pantry', ?, 'on', 'c2VjcmV0')`, household, string(hash)); err != nil {
		t.Fatal(err)
	}
	lan := stage1Do(handler, http.MethodGet, "/api/auth/session", rawCookie, nil, app.EntryLAN, "")
	if lan.Code != http.StatusOK || !strings.Contains(lan.Body.String(), `"kind":"lan"`) {
		t.Fatalf("lan session = %d %s", lan.Code, lan.Body.String())
	}
	lanLogin := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{"email": "ada@pantry.test", "password": "correct horse"}, app.EntryLAN, "")
	if lanLogin.Code != http.StatusNotFound {
		t.Fatalf("lan login = %d %s", lanLogin.Code, lanLogin.Body.String())
	}
	bearerLAN := stage1Request(http.MethodGet, "/api/auth/session", "", nil, app.EntryLAN, "")
	bearerLAN.Header.Set("Authorization", "Bearer "+rawCookie)
	bearerRec := httptest.NewRecorder()
	handler.ServeHTTP(bearerRec, bearerLAN)
	if bearerRec.Code != http.StatusUnauthorized || !strings.Contains(bearerRec.Body.String(), auth.SentenceUnauthorized) {
		t.Fatalf("lan bearer = %d %s", bearerRec.Code, bearerRec.Body.String())
	}

	retired := stage1Do(handler, http.MethodPost, "/api/household/household-password/retire", rawCookie, map[string]any{}, app.EntryPublic, "")
	if retired.Code != http.StatusOK || !strings.Contains(retired.Body.String(), `"householdPasswordEnabled":false`) {
		t.Fatalf("retire = %d %s", retired.Code, retired.Body.String())
	}
	lanAfter := stage1Do(handler, http.MethodGet, "/api/auth/session", "", nil, app.EntryLAN, "")
	if lanAfter.Code != http.StatusUnauthorized {
		t.Fatalf("lan after retire = %d %s", lanAfter.Code, lanAfter.Body.String())
	}
	basic := stage1Request(http.MethodGet, "/api/inventory", "", nil, app.EntryPublic, "")
	basic.SetBasicAuth("pantry", "shared-secret")
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != http.StatusUpgradeRequired || !strings.Contains(basicRec.Body.String(), auth.SentenceUpgrade) {
		t.Fatalf("basic after retire = %d %s", basicRec.Code, basicRec.Body.String())
	}

	svc.SetClock(func() time.Time { return time.Now().Add(181 * 24 * time.Hour) })
	capped := stage1Do(handler, http.MethodGet, "/api/auth/session", rawCookie, nil, app.EntryPublic, "")
	if capped.Code != http.StatusUnauthorized || !strings.Contains(capped.Body.String(), "Sign in to continue.") {
		t.Fatalf("180 day cap = %d %s", capped.Code, capped.Body.String())
	}
}

func TestStage1LoginRateLimit(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	if rec := stage1Do(handler, http.MethodGet, "/setup", "", nil, app.EntryLAN, ""); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d %s", rec.Code, rec.Body.String())
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Grace", "email": "grace@pantry.test", "password": "correct horse",
	}, app.EntryLAN, "192.0.2.8:9"); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 5; i++ {
		rec := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
			"email": "grace@pantry.test", "password": "wrong password",
		}, app.EntryPublic, "192.0.2.9:9")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	blocked := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "grace@pantry.test", "password": "wrong password",
	}, app.EntryPublic, "192.0.2.9:9")
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit = %d retry %q body %s", blocked.Code, blocked.Header().Get("Retry-After"), blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), auth.SentenceRate) {
		t.Fatalf("rate body %s", blocked.Body.String())
	}
}

func TestStage1FreshDatabaseKeepsLANOpenUntilRetirement(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))

	var passwordRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM household_password`).Scan(&passwordRows); err != nil {
		t.Fatal(err)
	}
	if passwordRows != 0 {
		t.Fatalf("household_password rows = %d, want none", passwordRows)
	}

	lanSession := stage1Do(handler, http.MethodGet, "/api/auth/session", "", nil, app.EntryLAN, "")
	if lanSession.Code != http.StatusOK || !strings.Contains(lanSession.Body.String(), `"kind":"lan"`) {
		t.Fatalf("fresh lan session = %d %s", lanSession.Code, lanSession.Body.String())
	}
	lanInventory := stage1Do(handler, http.MethodGet, "/api/inventory", "", nil, app.EntryLAN, "")
	if lanInventory.Code != http.StatusOK {
		t.Fatalf("fresh lan inventory = %d %s", lanInventory.Code, lanInventory.Body.String())
	}
	publicInventory := stage1Do(handler, http.MethodGet, "/api/inventory", "", nil, app.EntryPublic, "")
	if publicInventory.Code != http.StatusUnauthorized {
		t.Fatalf("public inventory = %d %s", publicInventory.Code, publicInventory.Body.String())
	}
	config := stage1Do(handler, http.MethodGet, "/api/config", "", nil, app.EntryPublic, "")
	if config.Code != http.StatusOK || !strings.Contains(config.Body.String(), `"householdPasswordSignIn":false`) {
		t.Fatalf("config = %d %s", config.Code, config.Body.String())
	}

	if rec := stage1Do(handler, http.MethodGet, "/setup", "", nil, app.EntryLAN, ""); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d %s", rec.Code, rec.Body.String())
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	created := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Ada Lovelace", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryLAN, "")
	if created.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", created.Code, created.Body.String())
	}
	login := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	rawCookie := cookieFrom(t, login, site.SessionCookie().Name)
	var household string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("shared-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key) VALUES (?, 'pantry', ?, 'on', 'c2VjcmV0')`, household, string(hash)); err != nil {
		t.Fatal(err)
	}
	stillOpen := stage1Do(handler, http.MethodGet, "/api/inventory", "", nil, app.EntryLAN, "")
	if stillOpen.Code != http.StatusOK {
		t.Fatalf("lan inventory while the password is on = %d %s", stillOpen.Code, stillOpen.Body.String())
	}
	signedIn := stage1Do(handler, http.MethodGet, "/api/config", "", nil, app.EntryPublic, "")
	if signedIn.Code != http.StatusOK || !strings.Contains(signedIn.Body.String(), `"householdPasswordSignIn":true`) {
		t.Fatalf("config with a stored password = %d %s", signedIn.Code, signedIn.Body.String())
	}

	retired := stage1Do(handler, http.MethodPost, "/api/household/household-password/retire", rawCookie, map[string]any{}, app.EntryPublic, "")
	if retired.Code != http.StatusOK || !strings.Contains(retired.Body.String(), `"householdPasswordEnabled":false`) {
		t.Fatalf("retire = %d %s", retired.Code, retired.Body.String())
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM household_password WHERE household_id = ?`, household).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "off" {
		t.Fatalf("household_password state = %q", state)
	}
	lanAfter := stage1Do(handler, http.MethodGet, "/api/auth/session", "", nil, app.EntryLAN, "")
	if lanAfter.Code != http.StatusUnauthorized {
		t.Fatalf("lan session after retire = %d %s", lanAfter.Code, lanAfter.Body.String())
	}
	inventoryAfter := stage1Do(handler, http.MethodGet, "/api/inventory", "", nil, app.EntryLAN, "")
	if inventoryAfter.Code != http.StatusUnauthorized {
		t.Fatalf("lan inventory after retire = %d %s", inventoryAfter.Code, inventoryAfter.Body.String())
	}
}

func stage1Do(handler http.Handler, method, path, cookie string, body any, entry app.Entry, remote string) *httptest.ResponseRecorder {
	req := stage1Request(method, path, cookie, body, entry, remote)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func stage1Request(method, path, cookie string, body any, entry app.Entry, remote string) *http.Request {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, reader)
	if remote == "" {
		remote = "192.0.2.10:443"
	}
	if remote != "omit-origin" {
		req.Header.Set("Origin", "https://pantry.test")
	} else {
		remote = "192.0.2.10:443"
	}
	req.RemoteAddr = remote
	req.Host = "pantry.test"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "__Host-stockbot_session", Value: cookie})
	}
	req = req.WithContext(app.WithEntry(req.Context(), entry))
	return req
}

func cookieFrom(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("missing cookie %s in %v", name, rec.Header().Values("Set-Cookie"))
	return ""
}

func TestStage1SetupFileMode(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	if err := svc.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(setupDir, "code"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
