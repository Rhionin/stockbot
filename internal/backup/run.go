package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
)

const (
	retentionAge = 14 * 24 * time.Hour
	overAgeLimit = 15 * 24 * time.Hour
	staleAfter   = 26 * time.Hour
	// maxClockSkew is how far the Pi clock may drift from R2 before a sweep
	// is refused. The Pi has no RTC. A clock that is days fast would treat a
	// backup that was just uploaded as already expired.
	maxClockSkew = 12 * time.Hour
)

// Runner performs one backup or a stale check.
type Runner struct {
	DBPath            string
	StageDir          string
	TmpfsLimit        int64
	Reason            string
	Recipients        string
	Store             ObjectStore
	Now               func() time.Time
	Sleep             func(time.Duration)
	BusyTimeout       time.Duration
	BusyRetries       int
	BusyRetryWait     time.Duration
	UploadRetries     int
	UploadRetryWait   time.Duration
	ImageDigest       string
	ToolVersion       string
	PrunePremigration bool
	IncludeSecrets    bool
	SecretsPaths      []string
	HealthURL         string
	Health            HealthPinger
	AfterSnapshot     func(path string) error

	// uploaded is every object this run has verified. verified is set once
	// the database object and its manifest are both in the bucket. Until
	// then a failure deletes uploaded, so a half-written run is not a fresh
	// backup. After that, a failure keeps them.
	uploaded []string
	verified bool
}

// Run snapshots, verifies, encrypts, uploads, sweeps, and checks freshness.
func (r *Runner) Run(ctx context.Context) (Manifest, error) {
	r.uploaded = nil
	r.verified = false
	started := r.now()
	manifest, err := r.run(ctx, started)
	if err != nil {
		// A failure before the database and its manifest are both verified
		// deletes that run's objects. A later failure (secrets, sweep, clock
		// skew, a delete that did not succeed, stale, or prune) keeps them,
		// pings, and returns the error.
		if !r.verified {
			r.deleteUploaded(ctx)
		}
		r.ping(ctx, false, "class="+AlertClass(err))
		return Manifest{}, err
	}
	line := fmt.Sprintf("bytes=%d duration=%s rows=%d", manifest.CiphertextSize, time.Duration(manifest.DurationMS)*time.Millisecond, manifest.RowTotal)
	r.ping(ctx, true, line)
	fmt.Printf("backup ok key=%s integrity=%s rows=%d staging=%s at=%s\n",
		manifest.ObjectKey, manifest.Integrity, manifest.RowTotal, manifest.Staging, denver(r.now()))
	return manifest, nil
}

func (r *Runner) run(ctx context.Context, started time.Time) (Manifest, error) {
	if r.Store == nil {
		return Manifest{}, errClass(ExitPreflight, "preflight", "the backup destination is not configured")
	}
	if r.DBPath == "" {
		return Manifest{}, errClass(ExitPreflight, "preflight", "the database path is missing")
	}
	dbBytes, err := databaseBytes(r.DBPath)
	if err != nil {
		return Manifest{}, err
	}
	dataKey, manifestKey, err := objectNames(started, r.Reason)
	if err != nil {
		return Manifest{}, err
	}
	limit := resolveTmpfsLimit(r.StageDir, r.TmpfsLimit)
	plan := ChooseStage(r.DBPath, r.StageDir, dbBytes, limit)
	log.Print(stageLog(plan))
	// A killed run can leave a plaintext copy on the data volume.
	plan.Cleanup()
	if err := prepareStage(plan); err != nil {
		return Manifest{}, err
	}
	defer plan.Cleanup()

	snap := plan.Dir + "/snap.db"
	if err := Snapshot(r.DBPath, snap, SnapshotOptions{
		BusyTimeout: r.busyTimeout(),
		Retries:     r.BusyRetries,
		RetryWait:   r.retryWait(),
		Sleep:       r.sleep,
	}); err != nil {
		return Manifest{}, err
	}
	if r.AfterSnapshot != nil {
		if err := r.AfterSnapshot(snap); err != nil {
			return Manifest{}, err
		}
	}
	info, err := Inspect(snap)
	if err != nil {
		return Manifest{}, err
	}
	if info.ForeignKeyCheck == "warning" {
		log.Print("backup snapshot: foreign key check reported rows; the copy was still uploaded")
	}

	body, sum, err := r.ciphertext(snap)
	if err != nil {
		return Manifest{}, err
	}

	if err := r.putVerified(ctx, dataKey, body, sum); err != nil {
		return Manifest{}, err
	}
	// The plaintext stays until the upload's size and sha256 match.
	// The deferred cleanup removes it when verification does not succeed.
	_ = os.Remove(snap)

	manifest := Manifest{
		ObjectKey:        dataKey,
		CiphertextSHA256: hex.EncodeToString(sum),
		CiphertextSize:   int64(len(body)),
		PlaintextSHA256:  info.PlaintextSHA256,
		PlaintextSize:    info.PlaintextSize,
		Integrity:        info.Integrity,
		ForeignKeyCheck:  info.ForeignKeyCheck,
		Tables:           info.Tables,
		LastMigration:    info.LastMigration,
		ToolVersion:      r.version(),
		ImageDigest:      r.ImageDigest,
		DurationMS:       r.now().Sub(started).Milliseconds(),
		Reason:           r.Reason,
		CreatedAt:        started.UTC().Format(time.RFC3339),
		PageCount:        info.PageCount,
		PageSize:         info.PageSize,
		RowTotal:         info.RowTotal,
		Staging:          plan.Kind,
	}
	raw, err := manifest.marshal()
	if err != nil {
		return Manifest{}, err
	}
	if err := r.putVerified(ctx, manifestKey, raw, nil); err != nil {
		return Manifest{}, err
	}
	r.verified = true
	if err := r.archiveSecrets(ctx, started); err != nil {
		return Manifest{}, err
	}
	if err := r.sweep(ctx, r.uploaded); err != nil {
		return Manifest{}, err
	}
	if err := r.CheckStale(ctx); err != nil {
		return Manifest{}, err
	}
	if r.PrunePremigration {
		if err := prunePremigration(r.DBPath); err != nil {
			return Manifest{}, err
		}
	}
	manifest.DurationMS = r.now().Sub(started).Milliseconds()
	return manifest, nil
}

func (r *Runner) ciphertext(path string) ([]byte, []byte, error) {
	// Age output is not deterministic, so the sha256 is known only after the
	// ciphertext exists. It stays in memory and is not written to a file.
	var buf bytes.Buffer
	if err := EncryptFile(path, r.Recipients, &buf); err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), sum[:], nil
}

func (r *Runner) putVerified(ctx context.Context, key string, body, sum []byte) error {
	if sum == nil {
		s := sha256.Sum256(body)
		sum = s[:]
	}
	attempts := r.uploadAttempts()
	wait := r.uploadWait()
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = r.putOnce(ctx, key, body, sum)
		if err == nil {
			return nil
		}
		if attempt+1 < attempts {
			r.sleep(wait)
			wait *= 2
		}
	}
	return err
}

func (r *Runner) putOnce(ctx context.Context, key string, body, sum []byte) error {
	if err := r.Store.Put(ctx, Upload{
		Key:    key,
		Body:   bytes.NewReader(body),
		Size:   int64(len(body)),
		SHA256: append([]byte(nil), sum...),
	}); err != nil {
		return err
	}
	info, err := r.Store.Head(ctx, key)
	if err != nil {
		_ = r.Store.Delete(ctx, key)
		return err
	}
	if info.Size != int64(len(body)) || !sameSHA(info.SHA256, sum) {
		_ = r.Store.Delete(ctx, key)
		return errClass(ExitUpload, "upload", "the uploaded backup did not match its size and sha256")
	}
	r.uploaded = append(r.uploaded, key)
	return nil
}

func (r *Runner) deleteUploaded(ctx context.Context) {
	if r.Store == nil {
		return
	}
	for _, key := range r.uploaded {
		_ = r.Store.Delete(ctx, key)
	}
	r.uploaded = nil
}

// sweep deletes managed objects older than 14 days. uploaded are the objects
// this run just verified; they are kept even when the Pi clock is wrong.
// The cutoff is R2's LastModified of those objects, not the Pi clock. A large
// skew refuses the sweep and deletes nothing.
func (r *Runner) sweep(ctx context.Context, uploaded []string) error {
	serverNow, err := r.backupServerTime(ctx, uploaded)
	if err != nil {
		return err
	}
	skew := serverNow.Sub(r.now())
	if skew < 0 {
		skew = -skew
	}
	if skew > maxClockSkew {
		return errClass(ExitSweep, "clock", "the clock is too far from the backup server")
	}
	objs, err := r.listManaged(ctx, managedPrefixes)
	if err != nil {
		return err
	}
	keep := map[string]struct{}{}
	for _, key := range uploaded {
		keep[key] = struct{}{}
	}
	for _, key := range newestDatabaseKeys(objs) {
		keep[key] = struct{}{}
	}
	cutoff := serverNow.Add(-retentionAge)
	failed := false
	for _, obj := range objs {
		if _, protected := keep[obj.Key]; protected {
			continue
		}
		if obj.LastModified.IsZero() || !obj.LastModified.Before(cutoff) {
			continue
		}
		if err := r.Store.Delete(ctx, obj.Key); err != nil {
			failed = true
			log.Printf("backup sweep: could not delete an expired object")
		}
	}
	if err := r.rejectOverAge(ctx, serverNow); err != nil {
		return err
	}
	if failed {
		return errClass(ExitSweep, "sweep", "could not delete an expired backup")
	}
	return nil
}

func (r *Runner) backupServerTime(ctx context.Context, uploaded []string) (time.Time, error) {
	if len(uploaded) == 0 {
		return time.Time{}, errClass(ExitSweep, "clock", "the backup server time is not known")
	}
	var server time.Time
	for _, key := range uploaded {
		info, err := r.Store.Head(ctx, key)
		if err != nil {
			return time.Time{}, errWrap(ExitSweep, "clock", "could not read the backup server time", err)
		}
		if info.LastModified.After(server) {
			server = info.LastModified
		}
	}
	if server.IsZero() {
		return time.Time{}, errClass(ExitSweep, "clock", "the backup server did not report a time")
	}
	return server, nil
}

func (r *Runner) listManaged(ctx context.Context, prefixes []string) ([]ObjectInfo, error) {
	var all []ObjectInfo
	for _, prefix := range prefixes {
		objs, err := r.Store.List(ctx, prefix)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	return all, nil
}

func (r *Runner) rejectOverAge(ctx context.Context, now time.Time) error {
	limit := now.Add(-overAgeLimit)
	for _, prefix := range managedPrefixes {
		objs, err := r.Store.List(ctx, prefix)
		if err != nil {
			return err
		}
		for _, obj := range objs {
			if !obj.LastModified.IsZero() && obj.LastModified.Before(limit) {
				return errClass(ExitSweep, "sweep", "a backup older than 15 days is still stored")
			}
		}
	}
	return nil
}

// CheckStale fails when the newest database backup is older than 26 hours
// on the backup server's clock, or when any managed object is older than
// 15 days. The Pi clock is not used: a clock that is a couple of hours
// fast must not treat a 25-hour-old backup as stale.
func (r *Runner) CheckStale(ctx context.Context) error {
	newest, found, err := r.newestDatabase(ctx)
	if err != nil {
		return err
	}
	now, err := r.serverClock(ctx)
	if err != nil {
		return err
	}
	if !found || newest.Before(now.Add(-staleAfter)) {
		return errClass(ExitSweep, "stale", "the newest backup is older than 26 hours")
	}
	if err := r.rejectOverAge(ctx, now); err != nil {
		var coded *Error
		if errors.As(err, &coded) && coded.Class == "sweep" {
			return errClass(ExitSweep, "stale", "a backup older than 15 days is still stored")
		}
		return err
	}
	return nil
}

// serverClock is the time R2 reported. The store learns it from a response
// Date header, or a test double supplies it directly. The Pi clock is only
// a fallback when the store has no server time at all.
func (r *Runner) serverClock(ctx context.Context) (time.Time, error) {
	type clock interface {
		ServerNow(context.Context) (time.Time, error)
	}
	if src, ok := r.Store.(clock); ok {
		server, err := src.ServerNow(ctx)
		if err != nil {
			return time.Time{}, err
		}
		if !server.IsZero() {
			return server, nil
		}
	}
	return r.now(), nil
}

func (r *Runner) newestDatabase(ctx context.Context) (time.Time, bool, error) {
	objs, err := r.listManaged(ctx, []string{"nightly/", "manual/"})
	if err != nil {
		return time.Time{}, false, err
	}
	best, ok := selectNewestVerified(objs)
	if !ok {
		return time.Time{}, false, nil
	}
	return best.LastModified, true, nil
}

// selectNewestVerified picks the newest database object that has its manifest.
// An upload that stopped before the manifest is not a successful backup.
func selectNewestVerified(objs []ObjectInfo) (ObjectInfo, bool) {
	present := make(map[string]struct{}, len(objs))
	for _, obj := range objs {
		present[obj.Key] = struct{}{}
	}
	var best ObjectInfo
	found := false
	for _, obj := range objs {
		if !verifiedDatabase(obj.Key, present) {
			continue
		}
		if !found || obj.LastModified.After(best.LastModified) {
			best = obj
			found = true
		}
	}
	return best, found
}

func verifiedDatabase(key string, present map[string]struct{}) bool {
	if !isDatabaseObject(key) {
		return false
	}
	mkey, err := manifestKeyFor(key)
	if err != nil {
		return false
	}
	_, ok := present[mkey]
	return ok
}

// newestDatabaseKeys are the newest database objects. The sweep never deletes
// them, even when they are the only copy and it is older than 14 days.
func newestDatabaseKeys(objs []ObjectInfo) []string {
	var newest time.Time
	var keys []string
	found := false
	for _, obj := range objs {
		if !isDatabaseObject(obj.Key) {
			continue
		}
		if !found || obj.LastModified.After(newest) {
			newest = obj.LastModified
			keys = []string{obj.Key}
			found = true
			continue
		}
		if obj.LastModified.Equal(newest) {
			keys = append(keys, obj.Key)
		}
	}
	return keys
}

func isDatabaseObject(key string) bool {
	return len(key) > len(".pantry.db.gz.age") && key[len(key)-len(".pantry.db.gz.age"):] == ".pantry.db.gz.age"
}

var managedPrefixes = []string{"nightly/", "manual/", "secrets/"}

func (r *Runner) ping(ctx context.Context, ok bool, body string) {
	if r.HealthURL == "" {
		return
	}
	pinger := r.Health
	if pinger == nil {
		pinger = HTTPPinger{}
	}
	if err := pinger.Ping(ctx, r.HealthURL, body, !ok); err != nil {
		log.Print("healthchecks ping failed")
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) sleep(d time.Duration) {
	if r.Sleep != nil {
		r.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (r *Runner) busyTimeout() time.Duration {
	if r.BusyTimeout > 0 {
		return r.BusyTimeout
	}
	return 10 * time.Second
}

func (r *Runner) retryWait() time.Duration {
	if r.BusyRetryWait > 0 {
		return r.BusyRetryWait
	}
	return 30 * time.Second
}

func (r *Runner) uploadAttempts() int {
	if r.UploadRetries > 0 {
		return r.UploadRetries
	}
	return 3
}

func (r *Runner) uploadWait() time.Duration {
	if r.UploadRetryWait > 0 {
		return r.UploadRetryWait
	}
	return time.Second
}

func (r *Runner) version() string {
	if r.ToolVersion != "" {
		return r.ToolVersion
	}
	return buildinfo.VisibleVersion()
}

func prunePremigration(dbPath string) error {
	dir := dirOf(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errWrap(ExitSweep, "prune", "could not read the database directory", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > len("pantry-pre-") && name[:len("pantry-pre-")] == "pantry-pre-" && len(name) > 3 && name[len(name)-3:] == ".db" {
			if err := os.Remove(dir + "/" + name); err != nil {
				return errWrap(ExitSweep, "prune", "could not remove a pre-migration copy", err)
			}
		}
	}
	return nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
