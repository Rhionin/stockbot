package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCodesPasswordsLimitsAndStreams(t *testing.T) {
	var directory *Service
	if directory.Kind() != "local" {
		t.Fatal(directory.Kind())
	}
	if err := (NopMailer{}).Send(context.Background(), "a@b.test", "subject", "body"); err != nil {
		t.Fatal(err)
	}
	setup, err := NewSetupCode()
	if err != nil {
		t.Fatal(err)
	}
	if norm, ok := NormalizeSetupCode(strings.ToLower(strings.ReplaceAll(setup, "-", ""))); !ok || norm != setup {
		t.Fatalf("setup norm %q %v", norm, ok)
	}
	if _, ok := NormalizeSetupCode("!!!!"); ok {
		t.Fatal("bad setup code normalized")
	}
	invite, err := NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := NormalizeInviteCode(strings.ToLower(invite)); !ok {
		t.Fatal("invite did not normalize")
	}
	badInvite := invite[:len(invite)-1] + "0"
	if badInvite == invite {
		badInvite = invite[:len(invite)-1] + "1"
	}
	if _, ok := NormalizeInviteCode(badInvite); ok {
		t.Fatal("bad invite check normalized")
	}
	if _, ok := NormalizeInviteCode("x"); ok {
		t.Fatal("short invite normalized")
	}
	pair, err := NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, well, check := NormalizePairingCode("  " + strings.ToLower(pair) + "\n"); !well || !check {
		t.Fatal("pairing did not normalize")
	}
	if _, well, check := NormalizePairingCode("SBP-OOOO-IIII-0"); !well || check {
		t.Fatalf("bad check well %v check %v", well, check)
	}
	if _, well, _ := NormalizePairingCode("nope"); well {
		t.Fatal("malformed pairing looked well formed")
	}
	session, err := NewSessionToken()
	if err != nil || !strings.HasPrefix(session, "sbs_") || HashOpaque(session) == "" {
		t.Fatalf("session %q", session)
	}
	device, err := NewDeviceToken()
	if err != nil || !strings.HasPrefix(device, "sbd_") || DevicePrefix(device) != device[:8] || DevicePrefix("short") != "short" {
		t.Fatalf("device %q prefix %q", device, DevicePrefix(device))
	}

	hash, err := HashPassword(context.Background(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(context.Background(), hash, "correct horse")
	if err != nil || !ok {
		t.Fatalf("verify %v %v", ok, err)
	}
	ok, err = VerifyPassword(context.Background(), "nope", "correct horse")
	if err != nil || ok {
		t.Fatalf("malformed verify %v %v", ok, err)
	}
	ok, err = VerifyPassword(context.Background(), "$argon2id$v=19$m=1,t=1,p=1$YQ$YQ", "correct horse")
	if err != nil || ok {
		t.Fatalf("short tag %v %v", ok, err)
	}
	if _, _, _, ok := parseArgonParams("m=1,t=0,p=1"); ok {
		t.Fatal("zero time parsed")
	}
	if _, _, _, ok := parseArgonParams("m=1,t=1,p=256"); ok {
		t.Fatal("wide threads parsed")
	}
	for i := 0; i < 2; i++ {
		rel, err := acquireArgon(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(rel)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := acquireArgon(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled argon %v", err)
	}

	if AsError(nil) != nil {
		t.Fatal("nil error became an error")
	}
	busy := AsError(errors.New("database is locked"))
	var ae *Error
	if !errors.As(busy, &ae) || ae.Code != CodeBusy || ae.Error() == "" || ae.Unwrap() == nil {
		t.Fatalf("busy %#v", busy)
	}
	full := AsError(errors.New("database or disk is full"))
	if !errors.As(full, &ae) || ae.Code != CodeNoSpace {
		t.Fatalf("full %#v", full)
	}
	plain := AsError(errors.New("other"))
	if !errors.As(plain, &ae) || ae.Code != "internal" {
		t.Fatalf("plain %#v", plain)
	}
	blank := &Error{Err: errors.New("hidden")}
	if blank.Error() != "hidden" {
		t.Fatal(blank.Error())
	}
	if fileErr(nil) != nil {
		t.Fatal("nil file error")
	}
	if got := fileErr(syscall.ENOSPC); got.Code != CodeNoSpace {
		t.Fatalf("no space %#v", got)
	}

	lim := NewLimiter()
	now := time.Now()
	if _, blocked := lim.Blocked(now, "", 1, time.Minute); blocked {
		t.Fatal("empty key blocked")
	}
	if _, blocked := lim.Hit(now.Add(-time.Hour), "k", 1, time.Minute); blocked {
		t.Fatal("old hit blocked")
	}
	if _, blocked := lim.Hit(now, "k", 1, time.Minute); blocked {
		t.Fatal("first hit blocked")
	}
	if _, blocked := lim.Hit(now, "k", 1, time.Minute); !blocked {
		t.Fatal("second hit was allowed")
	}
	lim.Reset("k")
	lim.Reset("")
	var none *Limiter
	none.Reset("k")
	if _, blocked := none.Blocked(now, "k", 1, time.Minute); blocked {
		t.Fatal("nil limiter blocked")
	}

	streams := NewStreams()
	if _, err := streams.Bind(nil, ""); err != nil {
		t.Fatal(err)
	}
	var empty *Streams
	if _, err := empty.Bind(context.Background(), "user"); err != nil {
		t.Fatal(err)
	}
	empty.Kick("user")
	if UserStreamKey("") != "" || !strings.HasPrefix(UserStreamKey("u"), "user:") {
		t.Fatal(UserStreamKey("u"))
	}
	cancels := make([]context.CancelFunc, 0, streamCap)
	for i := 0; i < streamCap; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := streams.Bind(ctx, "held"); err != nil {
			t.Fatal(err)
		}
		cancels = append(cancels, cancel)
	}
	if _, err := streams.Bind(context.Background(), "held"); err == nil {
		t.Fatal("eleventh stream was allowed")
	}
	streams.Kick("held")
	streams.Kick("")
	for _, cancel := range cancels {
		cancel()
	}
}

func TestSetupFileFailuresAndReuse(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	missing := NewService(db, filepath.Join(t.TempDir(), "absent"))
	if err := missing.Ensure(ctx); err == nil || missing.SetupHealth(ctx) != "unwritable" {
		t.Fatalf("missing dir %v health %q", err, missing.SetupHealth(ctx))
	}
	if err := missing.Boot(ctx); err == nil {
		t.Fatal("boot wrote a code without a directory")
	}
	missing.noteFile(syscall.ENOSPC)
	if missing.SetupHealth(ctx) != "no_space" {
		t.Fatalf("health %q", missing.SetupHealth(ctx))
	}

	dir := t.TempDir()
	svc := NewService(db, dir)
	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSetupCode(dir); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdminIssue(ctx, true); err == nil {
		t.Fatal("recover issued with no account")
	}
	svc.SetClock(func() time.Time { return time.Now().Add(48 * time.Hour) })
	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return time.Now() })

	blocked := t.TempDir()
	if err := os.Mkdir(filepath.Join(blocked, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	renameFail := NewService(db, blocked)
	if err := renameFail.AdminIssue(ctx, false); err == nil {
		t.Fatal("rename onto a directory succeeded")
	}
}

func TestSetupCommitFailureRollsBack(t *testing.T) {
	db := openAuthDB(t)
	dir := t.TempDir()
	svc := NewService(db, dir)
	prev := setupCommit
	setupCommit = func(*sql.Tx) error { return errors.New("commit failed") }
	t.Cleanup(func() { setupCommit = prev })
	if err := svc.Ensure(context.Background()); err == nil {
		t.Fatal("commit failure was ignored")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM setup_codes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("setup rows = %d", n)
	}
}
