package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"golang.org/x/crypto/bcrypt"
)

func TestImportHouseholdPasswordThenDropsTheFile(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.caddy")
	body := "keeper " + string(hash) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := "sample-session-secret-0123456789abcd"
	if err := ImportHouseholdPassword(context.Background(), conn, path, secret); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keeper $2a$04$not-a-real-hash-value-at-all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ImportHouseholdPassword(context.Background(), conn, path, "another-session-secret-0123456789"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	gate, err := HouseholdAuthFromDB(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.Configured || gate.Username != "keeper" {
		t.Fatalf("gate %+v", gate.Username)
	}
	if !gate.passwordMatches("keeper", "sample-passphrase-1") {
		t.Fatal("stored hash did not match")
	}
	if gate.passwordMatches("keeper", "other-passphrase") {
		t.Fatal("stored hash matched a different passphrase")
	}

	var stored string
	if err := conn.QueryRow(`SELECT pw_hash FROM household_password`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(hash) {
		t.Fatal("second import replaced the hash")
	}
}

func TestImportHouseholdPasswordRejectsABadFile(t *testing.T) {
	conn := migratedMemory(t)
	missing := filepath.Join(t.TempDir(), "missing")
	err := ImportHouseholdPassword(context.Background(), conn, missing, "sample-session-secret-0123456789abcd")
	if err == nil || !strings.Contains(err.Error(), "read the household password file") {
		t.Fatalf("missing: %v", err)
	}

	path := filepath.Join(t.TempDir(), "auth.caddy")
	if err := os.WriteFile(path, []byte("not a password file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd")
	if err == nil || !strings.Contains(err.Error(), "no bcrypt hash") {
		t.Fatalf("plain: %v", err)
	}

	if err := os.WriteFile(path, []byte("1keeper $2a$04$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd")
	if err == nil || !strings.Contains(err.Error(), "invalid username") {
		t.Fatalf("user: %v", err)
	}

	if err := os.WriteFile(path, []byte("keeper $2a$04$not-a-real-bcrypt-hash-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd")
	if err == nil || !strings.Contains(err.Error(), "not bcrypt") {
		t.Fatalf("hash: %v", err)
	}
}

func TestImportHouseholdPasswordWarnsWhenTheSessionSecretIsShort(t *testing.T) {
	conn := migratedMemory(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.caddy")
	if err := os.WriteFile(path, []byte("keeper "+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ImportHouseholdPassword(context.Background(), conn, path, "short")
	if err == nil || !strings.Contains(err.Error(), "PANTRY_SESSION_SECRET") {
		t.Fatalf("err = %v", err)
	}
	gate, err := HouseholdAuthFromDB(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.passwordMatches("keeper", "sample-passphrase-1") {
		t.Fatal("short secret dropped the hash")
	}
	if err := ImportHouseholdPassword(context.Background(), conn, path, "another-session-secret-0123456789"); err != nil {
		t.Fatalf("second import: %v", err)
	}
}

func TestHouseholdAuthFromDBFailsClosed(t *testing.T) {
	empty, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { empty.Close() })
	if _, err := HouseholdAuthFromDB(context.Background(), empty); err == nil || !strings.Contains(err.Error(), "could not read the household password") {
		t.Fatalf("unmigrated: %v", err)
	}
	if err := ImportHouseholdPassword(context.Background(), empty, filepath.Join(t.TempDir(), "auth.caddy"), "sample-session-secret-0123456789abcd"); err == nil {
		t.Fatal("import on an unmigrated database succeeded")
	}

	conn := migratedMemory(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.caddy")
	if err := os.WriteFile(path, []byte("keeper "+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := HouseholdAuthFromDB(ctx, conn); err == nil {
		t.Fatal("canceled read succeeded")
	}

	if _, err := conn.Exec(`UPDATE household_password SET state = 'off'`); err != nil {
		t.Fatal(err)
	}
	gate, err := HouseholdAuthFromDB(context.Background(), conn)
	if err == nil || !strings.Contains(err.Error(), "turned off") || gate == nil || gate.Configured {
		t.Fatalf("off: %v configured=%v", err, gate != nil && gate.Configured)
	}
	if _, err := conn.Exec(`UPDATE household_password SET state = 'on', session_key = '%%%'`); err != nil {
		t.Fatal(err)
	}
	gate, err = HouseholdAuthFromDB(context.Background(), conn)
	if err == nil || !strings.Contains(err.Error(), "could not start household login") || gate.Configured {
		t.Fatalf("key: %v", err)
	}
	if _, err := conn.Exec(`UPDATE household_password SET session_key = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := HouseholdAuthFromDB(context.Background(), conn); err == nil {
		t.Fatal("empty key was accepted")
	}
}

func TestImportHouseholdPasswordSkipsAnEmptyPath(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if err := ImportHouseholdPassword(context.Background(), conn, "", ""); err != nil {
		t.Fatal(err)
	}
	gate, err := HouseholdAuthFromDB(context.Background(), conn)
	if err == nil || gate == nil || gate.Configured {
		t.Fatalf("gate err=%v configured=%v", err, gate != nil && gate.Configured)
	}
}

func TestImportHouseholdPasswordReportsAStoreFailure(t *testing.T) {
	conn := migratedMemory(t)
	if _, err := conn.Exec(`CREATE TRIGGER reject_password BEFORE INSERT ON household_password BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.caddy")
	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keeper "+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd")
	if err == nil || !strings.Contains(err.Error(), "could not store the household password") {
		t.Fatalf("err = %v", err)
	}
}

func migratedMemory(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	return conn
}
