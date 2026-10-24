package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3ChecksumHeaderAndCredentials(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "ENVKEY123")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret-value")
	t.Setenv("R2_ACCESS_KEY_ID", "ENVKEY123")
	t.Setenv("R2_SECRET_ACCESS_KEY", "env-secret-value")
	fake := newFakeS3()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("age-encryption.org/v1\nsynthetic-ciphertext")
	sum := sha256.Sum256(body)
	ctx := context.Background()
	if err := store.Put(ctx, Upload{
		Key:    "nightly/a.pantry.db.gz.age",
		Body:   bytes.NewReader(body),
		Size:   int64(len(body)),
		SHA256: sum[:],
	}); err != nil {
		t.Fatalf("put: %v headers=%v", err, fake.headers)
	}
	if fake.checksum == "" {
		t.Fatalf("checksum header missing, headers=%v", fake.headers)
	}
	got, err := base64.StdEncoding.DecodeString(fake.checksum)
	if err != nil || !bytes.Equal(got, sum[:]) {
		t.Fatalf("checksum header %q", fake.checksum)
	}
	if strings.Contains(fake.auth, "ENVKEY123") || !strings.Contains(fake.auth, "FILEKEY123") {
		t.Fatalf("auth = %s", fake.auth)
	}
	if strings.Contains(fake.auth, "file-secret-value") || strings.Contains(fake.auth, "env-secret-value") {
		t.Fatal("secret access key appeared in the request")
	}
	info, err := store.Head(ctx, "nightly/a.pantry.db.gz.age")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(body)) || !bytes.Equal(info.SHA256, sum[:]) {
		t.Fatalf("head = %+v", info)
	}
	down, err := store.Get(ctx, "nightly/a.pantry.db.gz.age")
	if err != nil || !bytes.Equal(down, body) {
		t.Fatalf("get %v %q", err, down)
	}
}

func TestS3HeadMismatchFailsTheRun(t *testing.T) {
	fake := newFakeS3()
	fake.badHead = true
	srv := httptest.NewServer(fake)
	defer srv.Close()
	store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	path := fixtureDB(t)
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
	_, err = runner.Run(context.Background())
	if ExitCode(err) != ExitUpload {
		t.Fatalf("err = %v", err)
	}
	if fake.deletes == 0 {
		t.Fatal("mismatched object was not deleted")
	}
}

func TestS3PutRetriesServiceUnavailable(t *testing.T) {
	fake := newFakeS3()
	fake.putStatusLeft = 1
	srv := httptest.NewServer(fake)
	defer srv.Close()
	store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("age-encryption.org/v1\nretry")
	sum := sha256.Sum256(body)
	if err := store.Put(context.Background(), Upload{
		Key: "nightly/retry.pantry.db.gz.age", Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: sum[:],
	}); err != nil {
		t.Fatal(err)
	}
	if fake.putCalls < 2 {
		t.Fatalf("put calls = %d", fake.putCalls)
	}
}

func TestS3SHAMismatchDeletesAndHeadErrorDeletes(t *testing.T) {
	run := func(t *testing.T, tweak func(*fakeS3)) {
		t.Helper()
		fake := newFakeS3()
		tweak(fake)
		srv := httptest.NewServer(fake)
		defer srv.Close()
		store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		runner := &Runner{
			DBPath: fixtureDB(t), StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "nightly",
			Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
			UploadRetries: 1, UploadRetryWait: time.Millisecond,
		}
		_, err = runner.Run(context.Background())
		if ExitCode(err) != ExitUpload {
			t.Fatalf("err = %v", err)
		}
		if fake.deletes == 0 {
			t.Fatal("object was not deleted")
		}
		if len(fake.objs) != 0 {
			t.Fatalf("objects left: %d", len(fake.objs))
		}
	}
	run(t, func(fake *fakeS3) { fake.badSHA = true })
	run(t, func(fake *fakeS3) { fake.headStatus = http.StatusForbidden })
}

func TestS3SweepAndStale(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	fake := newFakeS3()
	fake.now = now
	srv := httptest.NewServer(fake)
	defer srv.Close()
	store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(key string, at time.Time) {
		t.Helper()
		body := []byte(key)
		sum := sha256.Sum256(body)
		fake.modified[key] = at
		if err := store.Put(ctx, Upload{Key: key, Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: sum[:]}); err != nil {
			t.Fatal(err)
		}
		fake.modified[key] = at
	}
	const just = "nightly/just.pantry.db.gz.age"
	put(just, now)
	put("nightly/just.manifest.json", now)
	put("nightly/fresh.pantry.db.gz.age", now.Add(-2*time.Hour))
	put("nightly/fresh.manifest.json", now.Add(-2*time.Hour))
	put("nightly/expired.pantry.db.gz.age", now.Add(-14*24*time.Hour-time.Hour))
	put("secrets/expired.tar.age", now.Add(-20*24*time.Hour))
	runner := &Runner{Store: store, Now: func() time.Time { return now }}
	if err := runner.sweep(ctx, []string{just}); err != nil {
		t.Fatal(err)
	}
	left, err := store.List(ctx, "nightly/")
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, obj := range left {
		kept[obj.Key] = true
	}
	if !kept[just] || !kept["nightly/fresh.pantry.db.gz.age"] || kept["nightly/expired.pantry.db.gz.age"] {
		t.Fatalf("left = %+v", left)
	}
	fake.modified[just] = now.Add(-27 * time.Hour)
	fake.modified["nightly/fresh.pantry.db.gz.age"] = now.Add(-27 * time.Hour)
	if err := runner.CheckStale(ctx); ExitCode(err) != ExitSweep {
		t.Fatalf("stale err = %v", err)
	}
	fake.modified["secrets/stuck.tar.age"] = now.Add(-16 * 24 * time.Hour)
	fake.objs["secrets/stuck.tar.age"] = fakeObj{body: []byte("x"), at: now.Add(-16 * 24 * time.Hour)}
	fake.failDelete["secrets/stuck.tar.age"] = true
	fake.modified[just] = now
	err = runner.sweep(ctx, []string{just})
	if ExitCode(err) != ExitSweep || !strings.Contains(err.Error(), "15 days") {
		t.Fatalf("over-age err = %v", err)
	}
}

type fakeObj struct {
	body []byte
	sum  []byte
	at   time.Time
}

type fakeS3 struct {
	mu            sync.Mutex
	objs          map[string]fakeObj
	modified      map[string]time.Time
	failDelete    map[string]bool
	checksum      string
	auth          string
	headers       http.Header
	badHead       bool
	badSHA        bool
	headStatus    int
	putStatusLeft int
	putCalls      int
	deletes       int
	now           time.Time
	omitDate      bool
}

func newFakeS3() *fakeS3 {
	return &fakeS3{
		objs:       map[string]fakeObj{},
		modified:   map[string]time.Time{},
		failDelete: map[string]bool{},
		now:        time.Now().UTC(),
	}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	omitDate := f.omitDate
	if auth := r.Header.Get("Authorization"); auth != "" {
		f.auth = auth
	}
	f.headers = r.Header.Clone()
	f.mu.Unlock()
	if omitDate {
		// A nil Date tells net/http not to insert one. The backup clock
		// has to fail closed when the server sends no time.
		w.Header()["Date"] = nil
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket == "" {
		http.Error(w, "missing bucket", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet && (key == "" || r.URL.Query().Get("list-type") == "2") {
		f.list(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		f.put(w, r, key)
	case http.MethodHead:
		f.head(w, key)
	case http.MethodGet:
		f.get(w, key)
	case http.MethodDelete:
		f.del(w, key)
	default:
		http.Error(w, "unsupported "+r.Method, http.StatusBadRequest)
	}
}

func (f *fakeS3) put(w http.ResponseWriter, r *http.Request, key string) {
	f.mu.Lock()
	f.putCalls++
	left := f.putStatusLeft
	if left > 0 {
		f.putStatusLeft--
	}
	f.mu.Unlock()
	if left > 0 {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	header := r.Header.Get("X-Amz-Checksum-Sha256")
	sum := sha256.Sum256(body)
	if header == "" {
		http.Error(w, "missing checksum", http.StatusBadRequest)
		return
	}
	got, err := base64.StdEncoding.DecodeString(header)
	if err != nil || !bytes.Equal(got, sum[:]) {
		http.Error(w, "bad checksum", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	at := f.now
	if mod, ok := f.modified[key]; ok {
		at = mod
	}
	f.objs[key] = fakeObj{body: body, sum: sum[:], at: at}
	f.checksum = header
	f.mu.Unlock()
	w.Header().Set("ETag", `"etag"`)
	w.Header().Set("x-amz-checksum-sha256", header)
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) head(w http.ResponseWriter, key string) {
	f.mu.Lock()
	obj, ok := f.objs[key]
	bad := f.badHead
	badSHA := f.badSHA
	status := f.headStatus
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, "unavailable", status)
		return
	}
	if !ok {
		http.NotFound(w, nil)
		return
	}
	size := len(obj.body)
	sum := append([]byte(nil), obj.sum...)
	if bad {
		size++
	}
	if badSHA && len(sum) > 0 {
		sum[0] ^= 0xff
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("x-amz-checksum-sha256", base64.StdEncoding.EncodeToString(sum))
	w.Header().Set("Last-Modified", obj.at.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) get(w http.ResponseWriter, key string) {
	f.mu.Lock()
	obj, ok := f.objs[key]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.body)))
	_, _ = w.Write(obj.body)
}

func (f *fakeS3) del(w http.ResponseWriter, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if f.failDelete[key] {
		http.Error(w, "locked", http.StatusConflict)
		return
	}
	delete(f.objs, key)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeS3) list(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for key := range f.objs {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	buf.WriteString(`<ListBucketResult>`)
	fmt.Fprintf(&buf, "<Prefix>%s</Prefix>", xmlEscape(prefix))
	fmt.Fprintf(&buf, "<KeyCount>%d</KeyCount>", len(keys))
	buf.WriteString(`<IsTruncated>false</IsTruncated>`)
	for _, key := range keys {
		obj := f.objs[key]
		at := obj.at
		if mod, ok := f.modified[key]; ok {
			at = mod
		}
		fmt.Fprintf(&buf, "<Contents><Key>%s</Key><LastModified>%s</LastModified><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>",
			xmlEscape(key), at.UTC().Format("2006-01-02T15:04:05.000Z"), len(obj.body))
	}
	buf.WriteString(`</ListBucketResult>`)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(buf.Bytes())
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func TestS3MissingDateFailsClosed(t *testing.T) {
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	fake := newFakeS3()
	fake.now = now
	fake.omitDate = true
	srv := httptest.NewServer(fake)
	defer srv.Close()
	store, err := NewS3Store(srv.URL, "pantry-pi-backups", "FILEKEY123", "file-secret-value", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(key string) {
		t.Helper()
		body := []byte(key)
		sum := sha256.Sum256(body)
		if err := store.Put(ctx, Upload{Key: key, Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: sum[:]}); err != nil {
			t.Fatal(err)
		}
	}
	// Fresh on the server, and only 2 hours old on the Pi. A fallback to
	// the Pi clock would accept it. No Date header must be a clock failure.
	put("nightly/fresh.pantry.db.gz.age")
	put("nightly/fresh.manifest.json")
	if _, err := store.ServerNow(ctx); ExitCode(err) != ExitSweep || AlertClass(err) != "clock" {
		t.Fatalf("ServerNow err = %v class %s", err, AlertClass(err))
	}
	runner := &Runner{Store: store, Now: func() time.Time { return now.Add(2 * time.Hour) }}
	if err := runner.CheckStale(ctx); ExitCode(err) != ExitSweep || AlertClass(err) != "clock" {
		t.Fatalf("CheckStale err = %v class %s", err, AlertClass(err))
	}
}

func TestHealthPingAndFilesystemStage(t *testing.T) {
	var pinged string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pinged = r.URL.Path + " " + string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	path := fixtureDB(t)
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), Reason: "nightly",
		Recipients: mustRecipient(t), Store: newMemStore(time.Now),
		BusyTimeout: time.Second, HealthURL: srv.URL + "/backup",
	}
	manifest, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Staging != "tmpfs" {
		t.Fatalf("staging = %s", manifest.Staging)
	}
	if !strings.Contains(pinged, "/backup") || strings.Contains(pinged, "Secret Household Name") {
		t.Fatalf("ping = %s", pinged)
	}
}

func TestForeignKeyWarningStillUploads(t *testing.T) {
	path := filepathJoin(t)
	db, err := openWritable(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))`,
		`INSERT INTO child (id, parent_id) VALUES (1, 99)`,
		`CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY)`,
		`INSERT INTO schema_migrations (filename) VALUES ('001_initial_schema.sql')`,
	} {
		if _, err := db.Exec(query); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()
	store := newMemStore(time.Now)
	runner := &Runner{
		DBPath: path, StageDir: t.TempDir(), TmpfsLimit: 1 << 30, Reason: "manual",
		Recipients: mustRecipient(t), Store: store, BusyTimeout: time.Second,
	}
	manifest, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ForeignKeyCheck != "warning" || manifest.Integrity != "ok" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestCLIVersionCleanupAndUsage(t *testing.T) {
	if code := CLI([]string{"version"}); code != ExitOK {
		t.Fatalf("version %d", code)
	}
	if code := CLI(nil); code != ExitUsage {
		t.Fatalf("usage %d", code)
	}
	path := fixtureDB(t)
	dir := filepathDir(path)
	if err := os.MkdirAll(dir+"/.backup-staging", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.backup-staging/snap.db", []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := CLI([]string{"cleanup-stage", "-db", path}); code != ExitOK {
		t.Fatalf("cleanup %d", code)
	}
	if _, err := os.Stat(dir + "/.backup-staging"); !os.IsNotExist(err) {
		t.Fatalf("stage remains: %v", err)
	}
}

func filepathJoin(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/pantry.db"
}

func filepathDir(path string) string {
	i := strings.LastIndex(path, "/")
	return path[:i]
}

func openWritable(path string) (*sql.DB, error) {
	return openSource(path, time.Second)
}
