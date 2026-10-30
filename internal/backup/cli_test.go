package backup

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func TestCLIRunFetchVerifyAndStale(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	settings := writeSettings(t, srv.URL)
	recipients := filepath.Join(t.TempDir(), "recipients")
	if err := os.WriteFile(recipients, []byte(identity.Recipient().String()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(secret, []byte("token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := fixtureDB(t)
	pre := filepath.Join(filepath.Dir(db), "pantry-pre-groups-keep.db")
	if err := os.WriteFile(pre, []byte("old-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout := captureFD(t, &os.Stdout)
	code := CLI([]string{
		"run",
		"-db", db,
		"-stage", t.TempDir(),
		"-tmpfs-limit", "1000000000",
		"-secrets", settings,
		"-recipients", recipients,
		"-reason", "drill",
		"-busy-timeout", "1s",
		"-busy-retries", "0",
		"-with-secrets",
		"-secrets-path", secret,
		"-image-digest", "sha256:test",
	})
	text := stdout()
	if code != ExitOK {
		t.Fatalf("run %d\n%s", code, text)
	}
	if !strings.Contains(text, "backup ok key=manual/") || !strings.Contains(text, "staging=tmpfs") {
		t.Fatalf("stdout = %s", text)
	}
	if _, err := os.Stat(pre); err != nil {
		t.Fatal("prune ran without --prune-premigration")
	}
	if strings.Contains(text, "Secret Household Name") || strings.Contains(text, "token-value") {
		t.Fatal("stdout included private data")
	}
	var sawSecrets bool
	for key, obj := range fake.objs {
		if strings.Contains(string(obj.body), "token-value") || strings.Contains(string(obj.body), "Secret Household Name") {
			t.Fatalf("object %s contains plaintext", key)
		}
		if strings.HasPrefix(key, "secrets/") && strings.HasSuffix(key, ".tar.age") {
			sawSecrets = true
		}
	}
	if !sawSecrets {
		t.Fatal("secrets object was not uploaded")
	}

	cipher := filepath.Join(t.TempDir(), "snapshot.age")
	manifest := filepath.Join(t.TempDir(), "snapshot.json")
	if code := CLI([]string{"fetch", "-secrets", settings, "-out", cipher, "-manifest-out", manifest}); code != ExitOK {
		t.Fatalf("fetch %d", code)
	}
	plain := filepath.Join(t.TempDir(), "restored.db")
	in, err := os.Open(cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(plain, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := DecryptGunzip(in, identity.String(), out); err != nil {
		out.Close()
		t.Fatal(err)
	}
	out.Close()
	if code := CLI([]string{"verify", "-db", plain, "-manifest", manifest}); code != ExitOK {
		t.Fatalf("verify %d", code)
	}
	if code := CLI([]string{"stale", "-secrets", settings}); code != ExitOK {
		t.Fatalf("stale %d", code)
	}
}

func TestCLIRejectsBadInput(t *testing.T) {
	if code := CLI([]string{"verify"}); code != ExitUsage {
		t.Fatalf("verify usage %d", code)
	}
	if code := CLI([]string{"fetch"}); code != ExitUsage {
		t.Fatalf("fetch usage %d", code)
	}
	if code := CLI([]string{"cleanup-stage"}); code != ExitUsage {
		t.Fatalf("cleanup usage %d", code)
	}
	if code := CLI([]string{"nope"}); code != ExitUsage {
		t.Fatalf("unknown %d", code)
	}
	if code := CLI([]string{"help"}); code != ExitOK {
		t.Fatalf("help %d", code)
	}
	settings := writeSettings(t, "http://127.0.0.1:1")
	recipients := filepath.Join(t.TempDir(), "recipients")
	if err := os.WriteFile(recipients, []byte(mustRecipient(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := captureFD(t, &os.Stderr)
	code := CLI([]string{
		"run", "-db", filepath.Join(t.TempDir(), "missing.db"),
		"-secrets", settings, "-recipients", recipients,
		"-busy-timeout", "1s", "-busy-retries", "0",
	})
	if code == ExitOK {
		t.Fatal("missing database was accepted")
	}
	if !strings.Contains(stderr(), "class=") {
		t.Fatal("failure did not name an error class")
	}
}

func TestFetchNewestAndConfigOverrides(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	ctx := t.Context()
	put := func(key string, at time.Time, body string) {
		t.Helper()
		store.now = func() time.Time { return at }
		if err := store.Put(ctx, Upload{Key: key, Body: strings.NewReader(body), Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	put("nightly/old.pantry.db.gz.age", now.Add(-2*time.Hour), "cipher-old")
	put("manual/new.manual.pantry.db.gz.age", now.Add(-time.Hour), "cipher-new")
	out := filepath.Join(t.TempDir(), "obj")
	manifest := filepath.Join(t.TempDir(), "manifest")
	if err := Fetch(store, "", out, manifest); err == nil {
		t.Fatal("missing manifest succeeded")
	}
	put("manual/new.manual.manifest.json", now, `{"ok":true}`)
	if err := Fetch(store, "", out, manifest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != "cipher-new" {
		t.Fatalf("fetched %q %v", got, err)
	}
	if err := Fetch(nil, "nightly/old.pantry.db.gz.age", out, ""); err == nil {
		t.Fatal("nil store")
	}
	if err := Fetch(store, "not-a-pantry-object", out, manifest); err == nil {
		t.Fatal("bad object name")
	}

	cfg := Config{AccountID: "abc123", AccessKeyID: "id", SecretAccessKey: "secret", Bucket: "pantry-pi-backups"}
	endpoint, err := cfg.EndpointURL()
	if err != nil || !strings.Contains(endpoint, "abc123.r2.cloudflarestorage.com") {
		t.Fatalf("endpoint %q %v", endpoint, err)
	}
	cfg = cfg.ApplyOverrides("other-bucket", "http://example.test", true, []string{"/later/path"})
	if cfg.Bucket != "other-bucket" || !cfg.IncludeSecrets || len(cfg.SecretsPaths) != 1 || cfg.SecretsPaths[0] != "/later/path" {
		t.Fatalf("override %+v", cfg)
	}
	if _, err := cfg.EndpointURL(); err != nil {
		t.Fatal(err)
	}
	if err := (Config{}).validate(); err == nil {
		t.Fatal("empty config")
	}
	var list stringList
	if err := list.Set("/a"); err != nil || list.String() != "/a" {
		t.Fatal(list)
	}
	(&Runner{}).sleep(0)
	if (&Runner{}).busyTimeout() != 10*time.Second || (&Runner{}).busyRetries() != 3 || (&Runner{}).retryWait() != 30*time.Second {
		t.Fatal("defaults")
	}
	if (&Runner{}).version() == "" || (&Runner{}).uploadAttempts() != 3 || (&Runner{}).uploadWait() != time.Second {
		t.Fatal("version or attempts")
	}
}

func TestSecretsPathRejectsMissingAndSymlink(t *testing.T) {
	runner := &Runner{
		DBPath: fixtureDB(t), StageDir: t.TempDir(), TmpfsLimit: 1 << 30,
		Reason: "nightly", Recipients: mustRecipient(t), Store: newMemStore(time.Now),
		BusyTimeout: time.Second, IncludeSecrets: true,
		SecretsPaths: []string{filepath.Join(t.TempDir(), "missing")},
	}
	if _, err := runner.Run(t.Context()); err == nil || ExitCode(err) == ExitOK {
		t.Fatal("missing secrets path")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	runner.SecretsPaths = []string{link}
	if _, err := runner.Run(t.Context()); err == nil {
		t.Fatal("symlink secrets path")
	}
	nested := filepath.Join(dir, "dir")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.SecretsPaths = []string{nested}
	if _, err := runner.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeBranches(t *testing.T) {
	var nilErr *Error
	if nilErr.Error() != "" {
		t.Fatal("nil error")
	}
	coded := errClass(ExitUpload, "upload", "nope").(*Error)
	if coded.Error() == "" || coded.Unwrap() == nil || ExitCode(nil) != ExitOK || ExitCode(coded) != ExitUpload {
		t.Fatal("coded error")
	}
	if ExitCode(io.EOF) != ExitPreflight || AlertClass(nil) != "backup" {
		t.Fatal("plain error")
	}
	if errWrap(ExitPreflight, "preflight", "plain", nil) == nil {
		t.Fatal("wrap nil")
	}
	if StageNeed(-5) != 0 {
		t.Fatal("negative size")
	}
	if err := CleanupStage(""); err == nil {
		t.Fatal("empty cleanup")
	}
	dir := t.TempDir()
	if _, err := databaseBytes(dir); err == nil {
		t.Fatal("directory database")
	}
	db := filepath.Join(dir, "pantry.db")
	if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", []byte("yy"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := databaseBytes(db)
	if err != nil || n != 3 {
		t.Fatalf("bytes %d %v", n, err)
	}
	if _, err := filesystemSize(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing filesystem")
	}
	if err := prepareStage(StagePlan{Dir: filepath.Join(db, "child")}); err == nil {
		t.Fatal("stage under a file")
	}
	if dirOf("/") != "/" || dirOf("pantry.db") != "." || dirOf("/data/pantry.db") != "/data" {
		t.Fatal("dirOf")
	}
	cfg := Config{AccessKeyID: "id", SecretAccessKey: "secret", Bucket: "NO", Endpoint: "http://example.test"}
	if err := cfg.validate(); err == nil {
		t.Fatal("bad bucket")
	}
	if _, err := NewS3Store("http://example.test", "", "id", "secret", nil); err == nil {
		t.Fatal("missing bucket")
	}
	if _, _, _, err := readUpload(Upload{Body: strings.NewReader("ab"), Size: 1}); err == nil {
		t.Fatal("size mismatch")
	}
	if err := writePrivate(dir, []byte("x")); err == nil {
		t.Fatal("write onto a directory")
	}
	if err := EncryptFile(filepath.Join(dir, "missing"), "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq", io.Discard); err == nil {
		t.Fatal("missing encrypt source")
	}
	if err := AgeEncrypt(strings.NewReader("x"), "not-a-key", io.Discard); err == nil {
		t.Fatal("bad recipient")
	}
	if err := DecryptGunzip(strings.NewReader("nope"), "AGE-SECRET-KEY-1notreal", io.Discard); err == nil {
		t.Fatal("bad decrypt")
	}
	if _, err := manifestKeyFor("notes.txt"); err == nil {
		t.Fatal("bad object name")
	}
	if _, err := parseManifest([]byte("{")); err == nil {
		t.Fatal("bad manifest")
	}
	fresh := newMemStore(time.Now)
	if err := (&Runner{Store: fresh}).CheckStale(t.Context()); ExitCode(err) != ExitSweep {
		t.Fatalf("empty bucket stale: %v", err)
	}
	if err := fresh.Put(t.Context(), Upload{Key: "nightly/now.pantry.db.gz.age", Body: strings.NewReader("c"), Size: 1}); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Put(t.Context(), Upload{Key: "nightly/now.manifest.json", Body: strings.NewReader("{}"), Size: 2}); err != nil {
		t.Fatal(err)
	}
	if err := (&Runner{Store: fresh}).CheckStale(t.Context()); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "dirsettings")
	if err := os.Mkdir(loose, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(loose); err == nil {
		t.Fatal("directory settings")
	}
	if err := EncryptReader(strings.NewReader("x"), "", io.Discard); err == nil {
		t.Fatal("empty recipient")
	}
	if (&Runner{UploadRetries: 2, UploadRetryWait: time.Millisecond}).uploadAttempts() != 2 {
		t.Fatal("upload attempts")
	}
	if (&Runner{ToolVersion: "test"}).version() != "test" {
		t.Fatal("tool version")
	}
	(StagePlan{}).Cleanup()
	if err := (HTTPPinger{}).Ping(t.Context(), "not a url", "x", true); err == nil {
		t.Fatal("bad health url")
	}
	if err := (HTTPPinger{}).Ping(t.Context(), "", "x", false); err != nil {
		t.Fatal(err)
	}
	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(rejected.Close)
	if err := (HTTPPinger{Client: rejected.Client()}).Ping(t.Context(), rejected.URL+"/hc", "class=upload", true); err == nil {
		t.Fatal("rejected healthchecks ping")
	}
	live := fixtureDB(t)
	info, err := Inspect(live)
	if err != nil {
		t.Fatal(err)
	}
	writeManifest := func(sha, migration, fk string, products int64) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "manifest.json")
		body := []byte(fmt.Sprintf(`{"plaintext_sha256":%q,"last_migration":%q,"foreign_key_check":%q,"tables":{"products":%d,"schema_migrations":1},"row_total":2}`, sha, migration, fk, products))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := Verify(live, writeManifest("nope", info.LastMigration, "ok", 1)); err == nil {
		t.Fatal("sha mismatch")
	}
	if err := Verify(live, writeManifest(info.PlaintextSHA256, "other.sql", "ok", 1)); err == nil {
		t.Fatal("migration mismatch")
	}
	if err := Verify(live, writeManifest(info.PlaintextSHA256, info.LastMigration, "warning", 1)); err == nil {
		t.Fatal("foreign key mismatch")
	}
	if err := Verify(live, writeManifest(info.PlaintextSHA256, info.LastMigration, "ok", 9)); err == nil {
		t.Fatal("count mismatch")
	}
}

func writeSettings(t *testing.T, endpoint string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "r2.env")
	body := "R2_ACCOUNT_ID=abc123\nR2_ACCESS_KEY_ID=FILEKEY123\nR2_SECRET_ACCESS_KEY=file-secret-value\nR2_BUCKET=pantry-pi-backups\nR2_ENDPOINT=" + endpoint + "\n"
	if err := os.WriteFile(path, []byte(body), 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureFD(t *testing.T, target **os.File) func() string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*target = w
	return func() string {
		_ = w.Close()
		*target = old
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		return buf.String()
	}
}
