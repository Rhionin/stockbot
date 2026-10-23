package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/Rhionin/stockbot/internal/app"
)

func TestChooseStageFitsInTmpfs(t *testing.T) {
	plan := ChooseStage("/data/pantry.db", "/stage", 1000, 2500)
	if plan.Kind != "tmpfs" || plan.Dir != "/stage" || plan.RemoveDir {
		t.Fatalf("plan = %+v", plan)
	}
	if StageNeed(1000) != 2500 {
		t.Fatalf("need = %d", StageNeed(1000))
	}
}

func TestChooseStageUsesVolumeWhenHeadroomDoesNotFit(t *testing.T) {
	plan := ChooseStage("/data/pantry.db", "/stage", 1000, 2499)
	if plan.Kind != "volume" || plan.Dir != "/data/.backup-staging" || !plan.RemoveDir {
		t.Fatalf("plan = %+v", plan)
	}
	// Equal to the limit still fits.
	if got := ChooseStage("/data/pantry.db", "/stage", 1000, 2500); got.Kind != "tmpfs" {
		t.Fatalf("boundary = %+v", got)
	}
	if got := ChooseStage("/data/pantry.db", "", 1, 1<<30); got.Kind != "volume" {
		t.Fatalf("no tmpfs dir = %+v", got)
	}
}

func TestQueryOnlyRejectsVacuumInto(t *testing.T) {
	path := fixtureDB(t)
	db, err := app.OpenSQLite(sqliteFileDSN(path, "_pragma=query_only(1)"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dest := filepath.Join(t.TempDir(), "snap.db")
	_, err = db.Exec(`VACUUM INTO ?`, dest)
	if err == nil {
		t.Fatal("query_only accepted VACUUM INTO; the snapshot connection should set it")
	}
}

func TestSnapshotSetsBusyTimeout(t *testing.T) {
	path := fixtureDB(t)
	db, err := openSource(path, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ms int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	if ms != 1500 {
		t.Fatalf("busy_timeout = %d", ms)
	}
}

func TestSnapshotWhileWritingJournalAndWAL(t *testing.T) {
	for _, mode := range []string{"delete", "wal"} {
		t.Run(mode, func(t *testing.T) {
			path := fixtureDB(t)
			db, err := app.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`PRAGMA journal_mode = ` + mode); err != nil {
				t.Fatal(err)
			}
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					_, _ = db.Exec(`INSERT INTO products (id, barcode, name) VALUES (?, '000', 'writer')`, time.Now().UnixNano())
				}
			}()
			dest := filepath.Join(t.TempDir(), "snap.db")
			err = Snapshot(path, dest, SnapshotOptions{BusyTimeout: 2 * time.Second, Retries: 2, RetryWait: time.Millisecond})
			close(stop)
			<-done
			if err != nil {
				t.Fatal(err)
			}
			info, err := Inspect(dest)
			if err != nil {
				t.Fatal(err)
			}
			if info.Integrity != "ok" || info.Tables["products"] < 1 {
				t.Fatalf("info = %+v", info)
			}
		})
	}
}

func TestSnapshotRetriesWhenBusy(t *testing.T) {
	path := fixtureDB(t)
	holder, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	released := false
	dest := filepath.Join(t.TempDir(), "snap.db")
	err = Snapshot(path, dest, SnapshotOptions{
		BusyTimeout: 50 * time.Millisecond,
		Retries:     3,
		RetryWait:   time.Millisecond,
		Sleep: func(time.Duration) {
			if !released {
				_, _ = holder.Exec(`COMMIT`)
				released = true
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("snapshot did not retry")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestRunStagesOnTmpfsAndVolume(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int64
		kind  string
	}{
		{name: "tmpfs", limit: 1 << 30, kind: "tmpfs"},
		{name: "volume", limit: 1, kind: "volume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fixtureDB(t)
			stage := t.TempDir()
			store := newMemStore(time.Now)
			var logs strings.Builder
			restore := captureLog(&logs)
			defer restore()
			recips, _ := testRecipient(t)
			runner := &Runner{
				DBPath:      path,
				StageDir:    stage,
				TmpfsLimit:  tc.limit,
				Reason:      "nightly",
				Recipients:  recips,
				Store:       store,
				BusyTimeout: time.Second,
				HealthURL:   "",
			}
			manifest, err := runner.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Staging != tc.kind || manifest.Integrity != "ok" {
				t.Fatalf("manifest = %+v", manifest)
			}
			if !strings.Contains(logs.String(), "backup staging: "+tc.kind) {
				t.Fatalf("log = %s", logs.String())
			}
			entries, _ := os.ReadDir(stage)
			if len(entries) != 0 {
				t.Fatalf("tmpfs stage not empty: %v", entries)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".backup-staging")); !os.IsNotExist(err) {
				t.Fatalf("volume stage still present: %v", err)
			}
			body := store.bodies()[0]
			if strings.HasPrefix(string(body), "SQLite format 3") || !strings.HasPrefix(string(body), "age-encryption.org/") {
				t.Fatalf("upload is not age ciphertext, prefix %q", body[:min(24, len(body))])
			}
			if bytesContains(body, []byte("Secret Household Name")) || bytesContains(body, []byte("000111222333")) {
				t.Fatal("plaintext leaked into the upload")
			}
			raw := store.bodyForSuffix(".manifest.json")
			if bytesContains(raw, []byte("Secret Household Name")) || bytesContains(raw, []byte("000111222333")) {
				t.Fatal("manifest contains household data")
			}
		})
	}
}

func TestIntegrityFailureUploadsNothing(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	runner := &Runner{
		DBPath:      path,
		StageDir:    t.TempDir(),
		TmpfsLimit:  1 << 30,
		Reason:      "manual",
		Recipients:  mustRecipient(t),
		Store:       store,
		BusyTimeout: time.Second,
		AfterSnapshot: func(snap string) error {
			data, err := os.ReadFile(snap)
			if err != nil {
				return err
			}
			idx := bytes.Index(data, []byte("CREATE TABLE"))
			if idx < 0 {
				return errClass(ExitIntegrity, "integrity", "snapshot has no schema text")
			}
			data[idx] = 'X'
			return os.WriteFile(snap, data, 0o600)
		},
	}
	_, err := runner.Run(context.Background())
	if ExitCode(err) != ExitIntegrity {
		t.Fatalf("err = %v code %d", err, ExitCode(err))
	}
	if store.putCount() != 0 {
		t.Fatalf("uploaded %d objects", store.putCount())
	}
}

func TestStageEmptyAfterPanic(t *testing.T) {
	path := fixtureDB(t)
	stage := t.TempDir()
	runner := &Runner{
		DBPath:      path,
		StageDir:    stage,
		TmpfsLimit:  1 << 30,
		Reason:      "nightly",
		Recipients:  mustRecipient(t),
		Store:       newMemStore(time.Now),
		BusyTimeout: time.Second,
		AfterSnapshot: func(string) error {
			panic("boom")
		},
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
		entries, _ := os.ReadDir(stage)
		if len(entries) != 0 {
			t.Fatalf("stage not empty: %v", entries)
		}
	}()
	_, _ = runner.Run(context.Background())
}

func TestVolumeStageRemovedWhenRunFails(t *testing.T) {
	path := fixtureDB(t)
	runner := &Runner{
		DBPath:      path,
		StageDir:    t.TempDir(),
		TmpfsLimit:  1,
		Reason:      "drill",
		Recipients:  mustRecipient(t),
		Store:       newMemStore(time.Now),
		BusyTimeout: time.Second,
		AfterSnapshot: func(string) error {
			panic("boom")
		},
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".backup-staging")); !os.IsNotExist(err) {
			t.Fatalf("volume stage remains: %v", err)
		}
	}()
	_, _ = runner.Run(context.Background())
}

func TestEncryptRoundTrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("synthetic-not-a-household")
	var cipher strings.Builder
	if err := EncryptReader(strings.NewReader(string(plain)), identity.Recipient().String(), &cipher); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := DecryptGunzip(strings.NewReader(cipher.String()), identity.String(), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(plain) {
		t.Fatalf("got %q", out.String())
	}
}

func TestSecretsArchiveOffByDefault(t *testing.T) {
	path := fixtureDB(t)
	secret := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(secret, []byte("token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMemStore(time.Now)
	runner := &Runner{
		DBPath:       path,
		StageDir:     t.TempDir(),
		TmpfsLimit:   1 << 30,
		Reason:       "nightly",
		Recipients:   mustRecipient(t),
		Store:        store,
		BusyTimeout:  time.Second,
		SecretsPaths: []string{secret},
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range store.keys() {
		if strings.HasPrefix(key, "secrets/") {
			t.Fatalf("secrets object uploaded: %s", key)
		}
	}
	runner.IncludeSecrets = true
	store2 := newMemStore(time.Now)
	runner.Store = store2
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, key := range store2.keys() {
		if strings.HasPrefix(key, "secrets/") && strings.HasSuffix(key, ".tar.age") {
			found = true
			body := store2.get(key)
			if bytesContains(body, []byte("token-value")) {
				t.Fatal("secrets archive uploaded plaintext")
			}
			if !strings.HasPrefix(string(body), "age-encryption.org/") {
				t.Fatal("secrets archive is not age ciphertext")
			}
		}
	}
	if !found {
		t.Fatal("secrets archive was not uploaded")
	}
}

func TestPrunePremigrationStaysOff(t *testing.T) {
	path := fixtureDB(t)
	pre := filepath.Join(filepath.Dir(path), "pantry-pre-groups-20200101T000000Z.db")
	if err := os.WriteFile(pre, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		DBPath:      path,
		StageDir:    t.TempDir(),
		TmpfsLimit:  1 << 30,
		Reason:      "pre-deploy",
		Recipients:  mustRecipient(t),
		Store:       newMemStore(time.Now),
		BusyTimeout: time.Second,
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pre); err != nil {
		t.Fatal(err)
	}
	runner.PrunePremigration = true
	runner.Store = newMemStore(time.Now)
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pre); !os.IsNotExist(err) {
		t.Fatalf("pre-migration copy remains: %v", err)
	}
}

func TestHeadMismatchFailsAndDeletes(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	store.headMismatch = true
	runner := &Runner{
		DBPath:          path,
		StageDir:        t.TempDir(),
		TmpfsLimit:      1 << 30,
		Reason:          "nightly",
		Recipients:      mustRecipient(t),
		Store:           store,
		BusyTimeout:     time.Second,
		UploadRetries:   1,
		UploadRetryWait: time.Millisecond,
	}
	_, err := runner.Run(context.Background())
	if ExitCode(err) != ExitUpload {
		t.Fatalf("err = %v", err)
	}
	if store.deleteCount() == 0 {
		t.Fatal("mismatched upload was not deleted")
	}
}

func TestSweepDeletesOnlyExpired(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	store.putAt("nightly/fresh.pantry.db.gz.age", []byte("a"), now.Add(-13*24*time.Hour))
	store.putAt("nightly/old.pantry.db.gz.age", []byte("b"), now.Add(-14*24*time.Hour-time.Hour))
	store.putAt("secrets/old.tar.age", []byte("c"), now.Add(-20*24*time.Hour))
	store.putAt("manual/keep.manual.pantry.db.gz.age", []byte("d"), now.Add(-time.Hour))
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	if err := runner.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.objs["nightly/fresh.pantry.db.gz.age"]; !ok {
		t.Fatal("fresh backup deleted")
	}
	if _, ok := store.objs["manual/keep.manual.pantry.db.gz.age"]; !ok {
		t.Fatal("recent manual backup deleted")
	}
	if _, ok := store.objs["nightly/old.pantry.db.gz.age"]; ok {
		t.Fatal("14 day backup remains")
	}
	if _, ok := store.objs["secrets/old.tar.age"]; ok {
		t.Fatal("old secrets object remains")
	}
}

func TestOverAgeRemainsExit30(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	store.putAt("nightly/stuck.pantry.db.gz.age", []byte("a"), now.Add(-16*24*time.Hour))
	store.failDelete["nightly/stuck.pantry.db.gz.age"] = true
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	err := runner.sweep(context.Background())
	if ExitCode(err) != ExitSweep {
		t.Fatalf("err = %v", err)
	}
}

func TestStaleBackup(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	store.putAt("nightly/old.pantry.db.gz.age", []byte("a"), now.Add(-27*time.Hour))
	pings := &recordPinger{}
	runner := &Runner{Store: store, Now: func() time.Time { return now }, HealthURL: "https://hc.example/ping", Health: pings}
	err := runner.CheckStale(context.Background())
	if ExitCode(err) != ExitSweep || AlertClass(err) != "stale" {
		t.Fatalf("err = %v class %s", err, AlertClass(err))
	}
	runner.ping(context.Background(), false, "class="+AlertClass(err))
	if !pings.failed {
		t.Fatal("stale check did not hit the failure hook")
	}
	store.putAt("nightly/new.pantry.db.gz.age", []byte("b"), now.Add(-time.Hour))
	if err := runner.CheckStale(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyMatchesManifest(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30,
		Reason: "nightly", Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
	}
	manifest, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manifest.marshal()
	if err != nil {
		t.Fatal(err)
	}
	manPath := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(manPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// The live database is not the snapshot. Build a fresh snapshot to verify.
	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := Snapshot(path, dest, SnapshotOptions{BusyTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(dest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.PlaintextSHA256 = info.PlaintextSHA256
	manifest.Tables = info.Tables
	manifest.LastMigration = info.LastMigration
	manifest.ForeignKeyCheck = info.ForeignKeyCheck
	raw, _ = manifest.marshal()
	if err := os.WriteFile(manPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, manPath); err != nil {
		t.Fatal(err)
	}
	manifest.Tables["products"]++
	raw, _ = manifest.marshal()
	_ = os.WriteFile(manPath, raw, 0o600)
	if err := Verify(dest, manPath); err == nil {
		t.Fatal("count mismatch was accepted")
	}
}

func TestLoadConfigIgnoresEnvAndRejectsLooseMode(t *testing.T) {
	t.Setenv("R2_SECRET_ACCESS_KEY", "from-env-secret")
	t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "r2.env")
	body := "R2_ACCOUNT_ID=abc123\nR2_ACCESS_KEY_ID=file-key\nR2_SECRET_ACCESS_KEY=file-secret-value\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || strings.Contains(err.Error(), "file-secret-value") {
		t.Fatalf("loose mode err = %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretAccessKey != "file-secret-value" || cfg.AccessKeyID != "file-key" || cfg.Bucket != DefaultBucket {
		t.Fatalf("cfg = %+v", cfg)
	}
	missing := "R2_ACCESS_KEY_ID=file-key\nR2_SECRET_ACCESS_KEY=file-secret-value\n"
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(missing), 0o400); err != nil {
		t.Fatal(err)
	}
	_, err = LoadConfig(path)
	if err == nil || strings.Contains(err.Error(), "file-secret-value") {
		t.Fatalf("missing account err = %v", err)
	}
}

func TestLoadRecipientsFromFileOrEnv(t *testing.T) {
	_, pub := testRecipient(t)
	if _, err := LoadRecipients("", ""); err == nil {
		t.Fatal("empty recipients accepted")
	}
	text, err := LoadRecipients("", pub)
	if err != nil || !strings.Contains(text, pub) {
		t.Fatalf("env recipient: %v %q", err, text)
	}
	path := filepath.Join(t.TempDir(), "recipients")
	if err := os.WriteFile(path, []byte("# comment\n\n"+pub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err = LoadRecipients(path, "")
	if err != nil || strings.Contains(text, "#") {
		t.Fatalf("file recipient: %v %q", err, text)
	}
	if err := os.WriteFile(path, []byte("AGE-SECRET-KEY-1notreal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecipients(path, ""); err == nil {
		t.Fatal("private key accepted")
	}
}

func fixtureDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE products (id TEXT PRIMARY KEY, barcode TEXT, name TEXT);
		INSERT INTO products (id, barcode, name) VALUES ('p1', '000111222333', 'Secret Household Name');
		CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, applied_at DATETIME);
		INSERT INTO schema_migrations (filename) VALUES ('021_scan_commit_counts.sql');
	`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func testRecipient(t *testing.T) (text, pub string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {})
	return id.Recipient().String() + "\n", id.Recipient().String()
}

func mustRecipient(t *testing.T) string {
	t.Helper()
	text, _ := testRecipient(t)
	return text
}

func bytesContains(b, sub []byte) bool {
	return strings.Contains(string(b), string(sub))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
