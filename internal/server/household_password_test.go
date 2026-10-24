package server

import (
	"context"
	"os"
	"path/filepath"
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
