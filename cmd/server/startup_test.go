package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestStartRejectsBadConfig(t *testing.T) {
	t.Setenv("APP_NAME", "bad\nname")
	err := start(make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "config:") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRejectsAMemoryDatabase(t *testing.T) {
	quietSite(t)
	t.Setenv("DB_PATH", ":memory:")
	t.Setenv("LAN_LISTEN_ADDR", "127.0.0.1:1")
	t.Setenv("PUBLIC_LISTEN_ADDR", "off")
	err := start(make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "open database:") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRejectsABusyPort(t *testing.T) {
	quietSite(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "pantry.db"))
	t.Setenv("LAN_LISTEN_ADDR", ln.Addr().String())
	t.Setenv("PUBLIC_LISTEN_ADDR", "off")
	t.Setenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP", "true")
	t.Setenv("DISABLE_KROGER", "true")
	err = start(make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "listen:") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartServesHealthUntilStopped(t *testing.T) {
	quietSite(t)
	lan := freeAddr(t)
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "pantry.db"))
	t.Setenv("LAN_LISTEN_ADDR", lan)
	t.Setenv("PUBLIC_LISTEN_ADDR", "off")
	t.Setenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP", "true")
	t.Setenv("DISABLE_KROGER", "true")
	t.Setenv("PRODUCT_CACHE_TTL", "nope")
	t.Setenv("PRODUCT_MISS_TTL", "nope")

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- start(stop) }()

	waitHealth(t, lan, done)
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestStartServesTheListener(t *testing.T) {
	quietSite(t)
	addr := freeAddr(t)
	dir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, "pantry-auth-fixture.txt")
	if err := os.WriteFile(authPath, []byte("keeper "+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_PATH", filepath.Join(dir, "pantry.db"))
	t.Setenv("ADDR", addr)
	t.Setenv("APP_BASE_URL", "https://pantry.example.test")
	t.Setenv("APP_NAME", "Pantry Test")
	t.Setenv("DISABLE_KROGER", "true")
	t.Setenv("PRODUCT_OPENER_USER_ID", "sample-user")
	t.Setenv("PRODUCT_OPENER_PASSWORD", "sample-password")
	t.Setenv("DEPLOY_HOOK_SECRET", "sample-deploy-secret")
	t.Setenv("PANTRY_SYNC_TOKEN", "sample-sync-token")
	t.Setenv("PANTRY_AUTH_FILE", authPath)
	t.Setenv("PANTRY_SESSION_SECRET", "sample-session-secret-0123456789abcd")
	t.Setenv("VAPID_PUBLIC_KEY", "not-a-key")
	t.Setenv("VAPID_PRIVATE_KEY", "not-a-key")
	t.Setenv("KROGER_BATCH_SIZE", "0")
	t.Setenv("KROGER_MODALITY", "SHIP")

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- start(stop) }()

	waitHealth(t, addr, done)
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestProcessMainStopsWhenTheProcessIsSignaled(t *testing.T) {
	quietSite(t)
	lan := freeAddr(t)
	dir := t.TempDir()
	t.Setenv("DB_PATH", filepath.Join(dir, "pantry.db"))
	t.Setenv("SETUP_DIR", dir)
	t.Setenv("LAN_LISTEN_ADDR", lan)
	t.Setenv("PUBLIC_LISTEN_ADDR", "off")
	t.Setenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP", "true")
	t.Setenv("DISABLE_KROGER", "true")
	t.Setenv("STOCK_IN_CONTROL_BARCODE", "STOCK_IN")
	t.Setenv("STOCK_OUT_CONTROL_BARCODE", "STOCK_OUT")
	t.Setenv("SCANNER_DEVICE", filepath.Join(dir, "missing-scanner"))
	t.Setenv("PANTRY_AUTH_FILE", filepath.Join(dir, "missing-auth"))
	t.Setenv("SENTRY_DSN", "https://public@sentry.example/1")
	t.Setenv("HEADLESS_USER_ID", "user-1")

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()
	t.Cleanup(func() {
		signal.Reset(os.Interrupt, syscall.SIGTERM)
	})
	waitHealth(t, lan, nil)
	health, err := http.Get("http://" + lan + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(health.Body)
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health = %d %s", health.StatusCode, body)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("main did not return after SIGTERM")
	}
}

func quietSite(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"APP_BASE_URL", "APP_NAME", "SESSION_COOKIE_NAME", "COOKIE_DOMAIN",
		"EMAIL_FROM", "LAN_PEOPLE", "EXPO_MIN_VERSION", "PUBLIC_HOST",
		"ADDR", "PUBLIC_LISTEN_ADDR", "LAN_LISTEN_ADDR",
		"PANTRY_AUTH_FILE", "PANTRY_SESSION_SECRET",
		"DISABLE_KROGER", "KROGER_CLIENT_ID", "KROGER_CLIENT_SECRET",
		"KROGER_REDIRECT_URI", "KROGER_MODALITY", "KROGER_BATCH_SIZE",
		"DISABLE_EXTERNAL_PRODUCT_LOOKUP",
		"PRODUCT_OPENER_USER_ID", "PRODUCT_OPENER_PASSWORD",
		"SENTRY_DSN", "VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT",
		"DEPLOY_HOOK_SECRET", "DEPLOY_TRIGGER_PATH", "PANTRY_SYNC_TOKEN",
		"PANTRY_RETAILER_API_KEY", "PANTRY_RETAILER_API_URL",
		"PRODUCT_CACHE_TTL", "PRODUCT_MISS_TTL",
		"SCAN_INPUT", "SCANNER_DEVICE", "HEADLESS_USER_ID",
		"DB_PATH",
	} {
		t.Setenv(key, "")
	}
	// Identical control barcodes keep the headless scanner from opening a device.
	t.Setenv("STOCK_IN_CONTROL_BARCODE", "STOP")
	t.Setenv("STOCK_OUT_CONTROL_BARCODE", "STOP")
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitHealth(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("start returned before /health: %v", err)
		default:
		}
		res, err := http.Get("http://" + addr + "/health")
		if err != nil {
			last = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			return
		}
		last = fmt.Errorf("status %d %s", res.StatusCode, body)
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET /health: %v", last)
}
