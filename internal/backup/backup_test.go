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
	plan := ChooseStage("/data/pantry.db", DefaultStageDir, 1000, 2500)
	if plan.Kind != "tmpfs" || plan.Dir != DefaultStageDir || plan.RemoveDir {
		t.Fatalf("plan = %+v", plan)
	}
	if StageNeed(1000) != 2500 {
		t.Fatalf("need = %d", StageNeed(1000))
	}
}

func TestChooseStageUsesVolumeWhenHeadroomDoesNotFit(t *testing.T) {
	plan := ChooseStage("/data/pantry.db", DefaultStageDir, 1000, 2499)
	if plan.Kind != "volume" || plan.Dir != "/data/.backup-staging" || !plan.RemoveDir {
		t.Fatalf("plan = %+v", plan)
	}
	// Equal to the limit still fits.
	if got := ChooseStage("/data/pantry.db", DefaultStageDir, 1000, 2500); got.Kind != "tmpfs" {
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
	var tempStore int
	if err := db.QueryRow(`PRAGMA temp_store`).Scan(&tempStore); err != nil {
		t.Fatal(err)
	}
	if tempStore != 2 { // MEMORY
		t.Fatalf("temp_store = %d", tempStore)
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
			// Match production busy-retries (3) with a shorter per-attempt
			// wait than nightly backup; delete journal mode needs the retries.
			err = Snapshot(path, dest, SnapshotOptions{BusyTimeout: 5 * time.Second, Retries: 3, RetryWait: 5 * time.Millisecond})
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

func TestHeadSHAMismatchDeletesWithoutSizeChange(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	store.shaMismatch = true
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
		UploadRetries: 1, UploadRetryWait: time.Millisecond,
	}
	_, err := runner.Run(context.Background())
	if ExitCode(err) != ExitUpload {
		t.Fatalf("err = %v", err)
	}
	if store.deleteCount() == 0 {
		t.Fatal("sha256 mismatch was not deleted")
	}
	for _, key := range store.keys() {
		if strings.HasSuffix(key, ".pantry.db.gz.age") {
			t.Fatalf("sha256 mismatch left %s", key)
		}
	}
}

func TestHeadErrorDeletesTheObject(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	store.failHead = true
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
		UploadRetries: 1, UploadRetryWait: time.Millisecond,
	}
	_, err := runner.Run(context.Background())
	if ExitCode(err) != ExitUpload {
		t.Fatalf("err = %v", err)
	}
	if store.deleteCount() == 0 {
		t.Fatal("object was kept after a HEAD error")
	}
	if len(store.keys()) != 0 {
		t.Fatalf("objects left after HEAD error: %v", store.keys())
	}
}

func TestUploadRetriesThenSucceeds(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	store.failPutLeft = 2
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
		UploadRetries: 3, UploadRetryWait: time.Millisecond,
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.putAttempts < 3 {
		t.Fatalf("put attempts = %d", store.putAttempts)
	}
}

func TestFailedRunDoesNotCountAsFresh(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	store.failPutContains = ".manifest.json"
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
		UploadRetries: 1, UploadRetryWait: time.Millisecond,
	}
	_, err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected the manifest upload to fail")
	}
	for _, key := range store.keys() {
		if strings.Contains(key, "pantry.db") || strings.HasSuffix(key, ".manifest.json") {
			t.Fatalf("failed run left %s", key)
		}
	}
	if ExitCode(runner.CheckStale(context.Background())) != ExitSweep {
		t.Fatal("failed run counted as a fresh backup")
	}
	store.putAt("nightly/orphan.pantry.db.gz.age", []byte("orphan"), time.Now())
	if _, err := newestKey(context.Background(), store); err == nil {
		t.Fatal("a backup without a manifest was selected")
	}
}

func TestIntegrityMismatchRowUploadsNothing(t *testing.T) {
	path := fixtureDB(t)
	store := newMemStore(time.Now)
	setupDone := false
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "manual",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
		AfterSnapshot: func(snap string) error {
			db, err := openWritable(snap)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`CREATE TABLE idx_items (id INTEGER PRIMARY KEY, name TEXT)`,
				`CREATE INDEX idx_items_name ON idx_items(name)`,
				`INSERT INTO idx_items (name) VALUES ('a')`,
				`PRAGMA writable_schema=ON`,
				`UPDATE sqlite_schema SET sql='CREATE INDEX idx_items_name ON idx_items(id)' WHERE name='idx_items_name'`,
			} {
				if _, err := db.Exec(query); err != nil {
					_ = db.Close()
					t.Fatal(err)
				}
			}
			// The connection that wrote the schema still has the old index
			// cached and reports ok. Close it and read the file back.
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			ro, err := openReadOnly(snap)
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Close()
			rows, err := ro.Query(`PRAGMA integrity_check`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var lines []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, line)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			// One non-ok line. A length check still passes, so only
			// lines[0] != "ok" on the snapshot path can refuse the upload.
			if len(lines) != 1 || lines[0] == "ok" {
				t.Fatalf("integrity fixture = %q", lines)
			}
			setupDone = true
			return nil
		},
	}
	_, err := runner.Run(context.Background())
	if !setupDone {
		t.Fatal("integrity fixture did not finish")
	}
	if err == nil || ExitCode(err) != ExitIntegrity || !strings.Contains(err.Error(), "the snapshot failed the integrity check") {
		t.Fatalf("snapshot path did not reject the upload: %v", err)
	}
	if store.putCount() != 0 {
		t.Fatalf("uploaded %d objects", store.putCount())
	}
}

func TestStartupCleansLeftoverStage(t *testing.T) {
	path := fixtureDB(t)
	stage := t.TempDir()
	leftover := filepath.Join(stage, "leftover.db")
	if err := os.WriteFile(leftover, []byte("plaintext-leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := false
	runner := &Runner{
		DBPath: path, StageDir: stage, TmpfsLimit: 1 << 30, Reason: "nightly",
		Recipients: mustRecipient(t), Store: newMemStore(time.Now), BusyTimeout: time.Second,
		AfterSnapshot: func(string) error {
			seen = true
			if _, err := os.Stat(leftover); !os.IsNotExist(err) {
				t.Fatalf("tmpfs leftover was still present: %v", err)
			}
			return nil
		},
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("snapshot hook did not run")
	}

	volume := filepath.Join(filepath.Dir(path), ".backup-staging")
	if err := os.MkdirAll(volume, 0o700); err != nil {
		t.Fatal(err)
	}
	volLeft := filepath.Join(volume, "leftover.db")
	if err := os.WriteFile(volLeft, []byte("plaintext-leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen = false
	runner.TmpfsLimit = 1
	runner.Store = newMemStore(time.Now)
	runner.AfterSnapshot = func(string) error {
		seen = true
		if _, err := os.Stat(volLeft); !os.IsNotExist(err) {
			t.Fatalf("volume leftover was still present: %v", err)
		}
		return nil
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("volume snapshot hook did not run")
	}
}

func TestSweepDeletesOnlyExpired(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	const just = "nightly/just.pantry.db.gz.age"
	store.putAt(just, []byte("new"), now)
	store.putAt("nightly/just.manifest.json", []byte("{}"), now)
	store.putAt("nightly/fresh.pantry.db.gz.age", []byte("a"), now.Add(-13*24*time.Hour))
	store.putAt("nightly/edge-keep.pantry.db.gz.age", []byte("k"), now.Add(-14*24*time.Hour))
	store.putAt("nightly/edge-drop.pantry.db.gz.age", []byte("d"), now.Add(-14*24*time.Hour-time.Second))
	store.putAt("secrets/old.tar.age", []byte("c"), now.Add(-20*24*time.Hour))
	store.putAt("secrets/edge-keep.tar.age", []byte("e"), now.Add(-14*24*time.Hour))
	store.putAt("manual/keep.manual.pantry.db.gz.age", []byte("m"), now.Add(-time.Hour))
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	if err := runner.sweep(context.Background(), []string{just}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{just, "nightly/fresh.pantry.db.gz.age", "nightly/edge-keep.pantry.db.gz.age", "manual/keep.manual.pantry.db.gz.age", "secrets/edge-keep.tar.age"} {
		if _, ok := store.objs[key]; !ok {
			t.Fatalf("kept object deleted: %s", key)
		}
	}
	if _, ok := store.objs["nightly/edge-drop.pantry.db.gz.age"]; ok {
		t.Fatal("object one second past 14 days remains")
	}
	if _, ok := store.objs["secrets/old.tar.age"]; ok {
		t.Fatal("old secrets object remains")
	}
}

func TestSweepKeepsNewestAndRefusesSkewedClock(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	const only = "nightly/only.pantry.db.gz.age"
	const just = "secrets/just.tar.age"
	store.putAt(just, []byte("s"), now)
	store.putAt(only, []byte("db"), now.Add(-20*24*time.Hour))
	store.putAt("nightly/only.manifest.json", []byte("{}"), now.Add(-20*24*time.Hour))
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	err := runner.sweep(context.Background(), []string{just})
	if ExitCode(err) != ExitSweep || !strings.Contains(err.Error(), "15 days") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := store.objs[only]; !ok {
		t.Fatal("newest database backup was deleted")
	}
	if _, ok := store.objs[just]; !ok {
		t.Fatal("object just uploaded was deleted")
	}

	skewed := newMemStore(func() time.Time { return now })
	const uploaded = "nightly/uploaded.pantry.db.gz.age"
	skewed.putAt(uploaded, []byte("u"), now)
	skewed.putAt("nightly/uploaded.manifest.json", []byte("{}"), now)
	skewed.putAt("nightly/ancient.pantry.db.gz.age", []byte("a"), now.Add(-30*24*time.Hour))
	fast := &Runner{Store: skewed, Now: func() time.Time { return now.Add(14 * 24 * time.Hour) }}
	err = fast.sweep(context.Background(), []string{uploaded})
	if ExitCode(err) != ExitSweep || AlertClass(err) != "clock" {
		t.Fatalf("skew err = %v class %s", err, AlertClass(err))
	}
	if skewed.deleteCount() != 0 {
		t.Fatal("skewed clock deleted objects")
	}
	if _, ok := skewed.objs[uploaded]; !ok {
		t.Fatal("uploaded object was deleted when the clock was fast")
	}
	if _, ok := skewed.objs["nightly/ancient.pantry.db.gz.age"]; !ok {
		t.Fatal("older backup was deleted when the clock was fast")
	}
}

func TestOverAgeBoundary(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	const just = "nightly/just.pantry.db.gz.age"
	exact := newMemStore(func() time.Time { return now })
	exact.putAt(just, []byte("j"), now)
	const edge = "secrets/edge.tar.age"
	exact.putAt(edge, []byte("e"), now.Add(-15*24*time.Hour))
	exact.failDelete[edge] = true
	runner := &Runner{Store: exact, Now: func() time.Time { return now }}
	err := runner.sweep(context.Background(), []string{just})
	if err == nil || strings.Contains(err.Error(), "15 days") {
		t.Fatalf("exactly 15 days was treated as over-age: %v", err)
	}
	late := newMemStore(func() time.Time { return now })
	late.putAt(just, []byte("j"), now)
	late.putAt(edge, []byte("e"), now.Add(-15*24*time.Hour-time.Second))
	late.failDelete[edge] = true
	runner.Store = late
	err = runner.sweep(context.Background(), []string{just})
	if ExitCode(err) != ExitSweep || !strings.Contains(err.Error(), "15 days") {
		t.Fatalf("one second past 15 days: %v", err)
	}
}

func TestOverAgeUsesServerClock(t *testing.T) {
	server := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	const uploaded = "secrets/just.tar.age"
	const kept = "nightly/kept.pantry.db.gz.age"

	// The Pi is 2 hours fast, inside the 12 hour skew limit. The only
	// database backup is 15 days minus 1 hour on the server, so it is kept
	// and it is not over-age. The same instant is past 15 days on the Pi.
	// rejectOverAge must use the server clock.
	fast := newMemStore(func() time.Time { return server })
	fast.putAt(uploaded, []byte("u"), server)
	fast.putAt(kept, []byte("k"), server.Add(-15*24*time.Hour+time.Hour))
	runner := &Runner{Store: fast, Now: func() time.Time { return server.Add(2 * time.Hour) }}
	if err := runner.sweep(context.Background(), []string{uploaded}); err != nil {
		t.Fatalf("over-age used the Pi clock: %v", err)
	}
	if _, ok := fast.objs[kept]; !ok {
		t.Fatal("a backup under 15 days on the server was deleted")
	}

	// The Pi is 2 hours slow. The newest database backup is one second past
	// 15 days on the server. The Pi clock would still call it under 15 days
	// and the sweep would succeed.
	slowStore := newMemStore(func() time.Time { return server })
	slowStore.putAt(uploaded, []byte("u"), server)
	slowStore.putAt(kept, []byte("k"), server.Add(-15*24*time.Hour-time.Second))
	slow := &Runner{Store: slowStore, Now: func() time.Time { return server.Add(-2 * time.Hour) }}
	err := slow.sweep(context.Background(), []string{uploaded})
	if ExitCode(err) != ExitSweep || !strings.Contains(err.Error(), "15 days") {
		t.Fatalf("over-age used the slow Pi clock: %v", err)
	}
	if _, ok := slowStore.objs[kept]; !ok {
		t.Fatal("the over-age backup was deleted instead of refused")
	}
}

func TestOverAgeRemainsExit30(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	const just = "nightly/just.pantry.db.gz.age"
	store.putAt(just, []byte("new"), now)
	store.putAt("nightly/stuck.pantry.db.gz.age", []byte("a"), now.Add(-16*24*time.Hour))
	store.failDelete["nightly/stuck.pantry.db.gz.age"] = true
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	err := runner.sweep(context.Background(), []string{just})
	if ExitCode(err) != ExitSweep {
		t.Fatalf("err = %v", err)
	}
}

func TestStaleBackup(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	store := newMemStore(func() time.Time { return now })
	putBackup := func(name string, at time.Time, manifest bool) {
		store.putAt("nightly/"+name+".pantry.db.gz.age", []byte(name), at)
		if manifest {
			store.putAt("nightly/"+name+".manifest.json", []byte("{}"), at)
		}
	}
	putBackup("old", now.Add(-27*time.Hour), true)
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
	putBackup("orphan", now.Add(-time.Hour), false)
	if err := runner.CheckStale(context.Background()); ExitCode(err) != ExitSweep {
		t.Fatal("a backup without a manifest counted as fresh")
	}
	orphanOnly := newMemStore(func() time.Time { return now })
	orphanOnly.putAt("nightly/orphan.pantry.db.gz.age", []byte("o"), now.Add(-time.Hour))
	if _, err := newestKey(context.Background(), orphanOnly); err == nil {
		t.Fatal("fetch selected a backup that has no manifest")
	}
	putBackup("edge-stale", now.Add(-26*time.Hour-time.Second), true)
	if err := runner.CheckStale(context.Background()); ExitCode(err) != ExitSweep {
		t.Fatal("a backup one second past 26 hours counted as fresh")
	}
	putBackup("edge-fresh", now.Add(-26*time.Hour+time.Second), true)
	if err := runner.CheckStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	exact := newMemStore(func() time.Time { return now })
	exact.putAt("nightly/exact.pantry.db.gz.age", []byte("exact"), now.Add(-26*time.Hour))
	exact.putAt("nightly/exact.manifest.json", []byte("{}"), now.Add(-26*time.Hour))
	runner.Store = exact
	if err := runner.CheckStale(context.Background()); err != nil {
		t.Fatal("a backup exactly 26 hours old was treated as stale")
	}

	// 25 hours old on the server. The Pi clock is 2 hours fast, which would
	// make this look 27 hours old if the stale check used the Pi.
	server := now
	skewed := newMemStore(func() time.Time { return server })
	skewed.putAt("nightly/young.pantry.db.gz.age", []byte("young"), server.Add(-25*time.Hour))
	skewed.putAt("nightly/young.manifest.json", []byte("{}"), server.Add(-25*time.Hour))
	fast := &Runner{Store: skewed, Now: func() time.Time { return server.Add(2 * time.Hour) }}
	if err := fast.CheckStale(context.Background()); err != nil {
		t.Fatal("a 25 hour backup was stale because the Pi clock is 2 hours fast")
	}
}

func TestSweepSkewLimit(t *testing.T) {
	server := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	const just = "nightly/just.pantry.db.gz.age"
	const old = "secrets/old.tar.age"
	atLimit := newMemStore(func() time.Time { return server })
	atLimit.putAt(just, []byte("j"), server)
	atLimit.putAt(old, []byte("o"), server.Add(-14*24*time.Hour-time.Second))
	runner := &Runner{Store: atLimit, Now: func() time.Time { return server.Add(12 * time.Hour) }}
	if err := runner.sweep(context.Background(), []string{just}); err != nil {
		t.Fatal(err)
	}
	if _, ok := atLimit.objs[old]; ok {
		t.Fatal("a 12 hour skew skipped the sweep")
	}

	over := newMemStore(func() time.Time { return server })
	over.putAt(just, []byte("j"), server)
	over.putAt(old, []byte("o"), server.Add(-30*24*time.Hour))
	runner = &Runner{Store: over, Now: func() time.Time { return server.Add(12*time.Hour + time.Second) }}
	err := runner.sweep(context.Background(), []string{just})
	if ExitCode(err) != ExitSweep || AlertClass(err) != "clock" {
		t.Fatalf("err = %v class %s", err, AlertClass(err))
	}
	if over.deleteCount() != 0 {
		t.Fatal("a skew one second past 12 hours deleted objects")
	}
	if _, ok := over.objs[just]; !ok {
		t.Fatal("the uploaded object was deleted when the clock was refused")
	}
}

func TestSweepCutoffUsesServerClock(t *testing.T) {
	server := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	const just = "nightly/just.pantry.db.gz.age"
	const edge = "secrets/edge.tar.age"

	// The Pi is 12 hours fast, which is still inside the skew limit. An
	// object exactly 14 days old by the server clock is kept. A cutoff of
	// Pi time minus 14 days would delete it.
	fast := newMemStore(func() time.Time { return server })
	fast.putAt(just, []byte("j"), server)
	fast.putAt(edge, []byte("e"), server.Add(-14*24*time.Hour))
	runner := &Runner{Store: fast, Now: func() time.Time { return server.Add(12 * time.Hour) }}
	if err := runner.sweep(context.Background(), []string{just}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fast.objs[edge]; !ok {
		t.Fatal("the sweep used the Pi clock and deleted a backup that is 14 days old on the server")
	}

	// The Pi is 12 hours slow. An object one second past 14 server days is
	// deleted. A cutoff of Pi time minus 14 days would keep it.
	slow := newMemStore(func() time.Time { return server })
	slow.putAt(just, []byte("j"), server)
	slow.putAt(edge, []byte("e"), server.Add(-14*24*time.Hour-time.Second))
	runner = &Runner{Store: slow, Now: func() time.Time { return server.Add(-12 * time.Hour) }}
	if err := runner.sweep(context.Background(), []string{just}); err != nil {
		t.Fatal(err)
	}
	if _, ok := slow.objs[edge]; ok {
		t.Fatal("the sweep used the slow Pi clock and kept an expired backup")
	}
}

func TestVerifiedBackupSurvivesLaterFailure(t *testing.T) {
	server := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name    string
		offset  time.Duration
		class   string
		prepare func(t *testing.T, path string, store *memStore) *Runner
	}{
		{name: "13h fast", offset: 13 * time.Hour, class: "clock"},
		{name: "13h slow", offset: -13 * time.Hour, class: "clock"},
		{name: "14 days fast", offset: 14 * 24 * time.Hour, class: "clock"},
		{name: "15 days slow", offset: -15 * 24 * time.Hour, class: "clock"},
		{
			name:   "failing delete",
			offset: 0,
			class:  "sweep",
			prepare: func(t *testing.T, path string, store *memStore) *Runner {
				const old = "nightly/old.pantry.db.gz.age"
				at := server.Add(-14*24*time.Hour - time.Second)
				store.putAt(old, []byte("old"), at)
				store.putAt("nightly/old.manifest.json", []byte("{}"), at)
				store.failDelete[old] = true
				return nil
			},
		},
		{
			name:   "secrets failure",
			offset: 0,
			class:  "secrets",
			prepare: func(t *testing.T, path string, store *memStore) *Runner {
				return &Runner{IncludeSecrets: true, SecretsPaths: []string{filepath.Join(t.TempDir(), "missing-secret")}}
			},
		},
		{
			name:   "prune failure",
			offset: 0,
			class:  "prune",
			prepare: func(t *testing.T, path string, store *memStore) *Runner {
				pre := filepath.Join(filepath.Dir(path), "pantry-pre-groups.db")
				if err := os.Mkdir(pre, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(pre, "child"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				return &Runner{PrunePremigration: true}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := fixtureDB(t)
			pi := server.Add(tc.offset)
			store := newMemStore(func() time.Time { return server })
			pings := &recordPinger{}
			runner := &Runner{
				DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
				Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
				Now: func() time.Time { return pi }, UploadRetries: 1, UploadRetryWait: time.Millisecond,
				HealthURL: "https://hc.example/backup", Health: pings,
			}
			if tc.prepare != nil {
				extra := tc.prepare(t, path, store)
				if extra != nil {
					runner.IncludeSecrets = extra.IncludeSecrets
					runner.SecretsPaths = extra.SecretsPaths
					runner.PrunePremigration = extra.PrunePremigration
				}
			}
			_, err := runner.Run(context.Background())
			if err == nil || AlertClass(err) != tc.class {
				t.Fatalf("err = %v class %s", err, AlertClass(err))
			}
			if !pings.failed || !strings.Contains(pings.body, "class="+tc.class) {
				t.Fatalf("failure ping = %+v", pings)
			}
			dataKey, manifestKey, err := objectNames(pi, "nightly")
			if err != nil {
				t.Fatal(err)
			}
			if store.get(dataKey) == nil || store.get(manifestKey) == nil {
				t.Fatalf("tonight's backup is missing: %v", store.keys())
			}
		})
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
