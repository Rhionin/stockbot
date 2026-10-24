package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyRelease(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("#!/bin/sh\necho ok\n")
	sum, sig := signRelease(priv, body)
	if err := VerifyRelease(pub, body, sum, sig); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey(hex.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey("   "); err == nil {
		t.Fatal("empty key")
	}
	if _, err := ParsePublicKey("zzzz"); err == nil {
		t.Fatal("bad key")
	}
	tampered := append([]byte{}, body...)
	tampered[0] ^= 0xff
	if err := VerifyRelease(pub, tampered, sum, sig); err == nil {
		t.Fatal("tampered artifact accepted")
	}
	_, priv2, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sum2, sig2 := signRelease(priv2, body)
	if err := VerifyRelease(pub, body, sum2, sig2); err == nil {
		t.Fatal("wrong key accepted")
	}
	if err := VerifyRelease(pub, body, "00", sig); err == nil {
		t.Fatal("bad digest accepted")
	}
	if err := VerifyRelease(pub, nil, sum, sig); err == nil {
		t.Fatal("empty artifact")
	}
	if err := VerifyRelease(nil, body, sum, sig); err == nil {
		t.Fatal("missing key")
	}
	if err := VerifyRelease(pub, body, sum, "%%%"); err == nil {
		t.Fatal("bad signature encoding")
	}
	if _, _, err := stageRelease(t.TempDir(), "../evil", body); err == nil {
		t.Fatal("version with a path was stored")
	}
}

func TestSignedUpdateRollbackWithin120s(t *testing.T) {
	api := &fakeAPI{}
	now := time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC)
	h, logs := newTestHub(t, api, func(o *Options) {
		o.Now = func() time.Time { return now }
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	h.pub = pub
	good := []byte("#!/bin/sh\nexit 0\n")
	bad := []byte("#!/bin/sh\nexit 1\n")
	api.artifact = good

	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.1.0", h.ServerURL(), api, good)); err != nil {
		t.Fatal(err)
	}
	if h.Version() != "1.1.0" || h.PendingVersion() != "1.1.0" {
		t.Fatalf("version %s pending %s", h.Version(), h.PendingVersion())
	}
	if target := currentTarget(t, h.releaseRoot); !strings.Contains(target, "1.1.0") {
		t.Fatalf("current -> %s", target)
	}
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.PendingVersion() != "" {
		t.Fatal("healthy heartbeat left the watchdog armed")
	}

	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.2.0", h.ServerURL(), api, good)); err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.3.0", h.ServerURL(), api, good)); err != nil {
		t.Fatal(err)
	}
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.releaseRoot, "releases", "1.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old release kept: %v", err)
	}

	stagedAt := now
	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.4.0", h.ServerURL(), api, good)); err != nil {
		t.Fatal(err)
	}
	if rolled, err := h.EvaluateWatchdog(stagedAt.Add(WatchdogLimit - time.Second)); err != nil || rolled {
		t.Fatalf("early watchdog rolled=%v %v", rolled, err)
	}
	if currentTarget(t, h.releaseRoot) == "" || !strings.Contains(currentTarget(t, h.releaseRoot), "1.4.0") {
		t.Fatal("update was rolled back before the deadline")
	}
	rolled, err := h.EvaluateWatchdog(stagedAt.Add(WatchdogLimit))
	if err != nil || !rolled {
		t.Fatalf("deadline rollback = %v %v", rolled, err)
	}
	if strings.Contains(currentTarget(t, h.releaseRoot), "1.4.0") {
		t.Fatal("failed update is still current")
	}
	if !strings.Contains(currentTarget(t, h.releaseRoot), "1.3.0") {
		t.Fatalf("current = %s", currentTarget(t, h.releaseRoot))
	}
	if h.Version() != "1.3.0" || !h.RollbackPending() {
		t.Fatalf("version %s rollback %v", h.Version(), h.RollbackPending())
	}
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := api.beats[len(api.beats)-1]
	if !last.Rollback || last.Version != "1.3.0" {
		t.Fatalf("rollback heartbeat = %+v", last)
	}
	if h.RollbackPending() {
		t.Fatal("rollback flag was not cleared")
	}
	if strings.Contains(logs.String(), api.token) {
		t.Fatal("logs included the token")
	}

	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.5.0", h.ServerURL(), api, bad)); err == nil {
		t.Fatal("failed self-check was activated")
	}
	if strings.Contains(currentTarget(t, h.releaseRoot), "1.5.0") || !h.RollbackPending() {
		t.Fatalf("self-check rollback current=%s flag=%v", currentTarget(t, h.releaseRoot), h.RollbackPending())
	}

	rel := releaseFor(t, priv, "1.6.0", h.ServerURL(), api, good)
	api.artifact = append([]byte(nil), good...)
	api.artifact[0] ^= 0xff
	if err := h.ApplyUpdate(context.Background(), rel); err == nil {
		t.Fatal("tampered download was installed")
	}
	_, priv2, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv2, "1.7.0", h.ServerURL(), api, good)); err == nil {
		t.Fatal("wrong key was installed")
	}

	h.pub = nil
	if err := h.ApplyUpdate(context.Background(), rel); err == nil {
		t.Fatal("update without a key")
	}
	root := h.releaseRoot
	h.releaseRoot = ""
	h.pub = pub
	if err := h.ApplyUpdate(context.Background(), releaseFor(t, priv, "1.8.0", h.ServerURL(), api, good)); err == nil {
		t.Fatal("update without a directory")
	}
	h.releaseRoot = root
	if err := defaultSelfCheck(context.Background(), ""); err == nil {
		t.Fatal("empty self-check")
	}
}

func TestTokenRotationIsDurableBeforeReuse(t *testing.T) {
	api := &fakeAPI{rotate: true}
	h, logs := newTestHub(t, api, nil)
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	old := api.token
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(h.opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(api.nextToken)) || bytes.Contains(raw, []byte(old)) {
		t.Fatal("rotated token was not the one on disk")
	}
	if strings.Contains(logs.String(), api.nextToken) || strings.Contains(logs.String(), old) {
		t.Fatal("logs included a token")
	}
	api.rotate = false
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := api.auths[len(api.auths)-1]; got != "Bearer "+api.nextToken {
		t.Fatalf("next heartbeat auth = %s", got)
	}
	api.failRotate = true
	api.rotate = true
	if err := h.Beat(context.Background()); err == nil {
		t.Fatal("rotate failure was ignored")
	}
	raw, _ = os.ReadFile(h.opts.StatePath)
	if !bytes.Contains(raw, []byte(api.nextToken)) {
		t.Fatal("failed rotation discarded the current token")
	}
}

func TestRestartedBootAge(t *testing.T) {
	api := &fakeAPI{}
	var mono int64 = 5000
	h, _ := newTestHub(t, api, func(o *Options) {
		o.BootID = "boot-a"
		o.Mono = func() int64 { return mono }
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "123456789012"); err != nil {
		t.Fatal(err)
	}
	opts := h.opts
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	mono = 1000
	opts.BootID = "boot-b"
	opts.Mono = func() int64 { return mono }
	h2, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if err := h2.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.batches) != 1 || api.batches[0].Scans[0].AgeMs != 1000 {
		t.Fatalf("restarted age = %+v", api.batches)
	}
}

func TestClientErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			_, _ = w.Write(bytes.Repeat([]byte("a"), maxArtifact+1))
		case "/date":
			w.Header().Set("Retry-After", time.Now().Add(2*time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		case "/past":
			w.Header().Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		case pairPath:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"device_revoked"}`))
		case heartbeatPath:
			_, _ = w.Write([]byte(`{`))
		case rotatePath:
			_ = json.NewEncoder(w).Encode(map[string]string{"token": ""})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/", HTTP: srv.Client()}
	if _, err := c.Download(context.Background(), srv.URL+"/big"); err == nil {
		t.Fatal("oversized update")
	}
	var status *StatusError
	if err := c.do(context.Background(), http.MethodPost, "/date", "sbd_x", beatRequest{Version: "1"}, &beatResponse{}); !errors.As(err, &status) || status.RetryAfter <= 0 || !retryable(err) {
		t.Fatalf("date retry = %v", err)
	}
	if err := c.do(context.Background(), http.MethodPost, "/past", "sbd_x", beatRequest{}, &beatResponse{}); !errors.As(err, &status) || status.RetryAfter != 0 {
		t.Fatalf("past retry = %v", err)
	}
	if _, err := c.Heartbeat(context.Background(), "sbd_x", beatRequest{Version: "1"}); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("bad json = %v", err)
	}
	if _, err := c.Pair(context.Background(), "SBP-0123-ABCD-G", "Counter", "pi_hub", "1", "boot"); !errors.As(err, &status) || !revoked(err) || !strings.Contains(err.Error(), "device_revoked") {
		t.Fatalf("revoked = %v", err)
	}
	if _, err := c.Rotate(context.Background(), "sbd_x"); err == nil {
		t.Fatal("empty rotation token")
	}
	if _, err := c.Download(context.Background(), "://bad"); err == nil {
		t.Fatal("bad url")
	}
	if retryable(context.Canceled) || retryable(context.DeadlineExceeded) {
		t.Fatal("cancel is retryable")
	}
	plain := errors.New("nope")
	if retryable(plain) || revoked(plain) || retryAfterOf(plain) != 0 {
		t.Fatal("plain error classified wrong")
	}
	if got := (&StatusError{Status: 500}).Error(); !strings.Contains(got, "500") {
		t.Fatal(got)
	}
	if retryAfterHeader("12") != 12*time.Second || retryAfterHeader("") != 0 || retryAfterHeader("nope") != 0 {
		t.Fatal("retry-after parse")
	}
}

func releaseFor(t *testing.T, priv ed25519.PrivateKey, version, base string, api *fakeAPI, body []byte) Release {
	t.Helper()
	api.artifact = append([]byte(nil), body...)
	sum, sig := signRelease(priv, body)
	return Release{Version: version, URL: base + "/artifact", SHA256: sum, Sig: sig}
}

func currentTarget(t *testing.T, root string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		return ""
	}
	return target
}
