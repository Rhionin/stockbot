package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestAdminSetupCodeIssuesAndRecovers(t *testing.T) {
	if err := runAdmin(nil); err == nil {
		t.Fatal("missing command succeeded")
	}
	if err := runAdmin([]string{"other"}); err == nil {
		t.Fatal("unknown command succeeded")
	}
	if err := runAdmin([]string{"setup-code", "--later"}); err == nil {
		t.Fatal("unknown flag succeeded")
	}

	root := t.TempDir()
	dbPath := filepath.Join(root, "pantry.db")
	setupDir := filepath.Join(root, "setup")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("SETUP_DIR", filepath.Join(root, "missing"))
	if err := runAdmin([]string{"setup-code", "--recover"}); err == nil {
		t.Fatal("recover succeeded with no account")
	}
	if err := os.Mkdir(setupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SETUP_DIR", setupDir)
	if err := runAdmin([]string{"setup-code"}); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, readDB, err := app.OpenPantry(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
		readDB.Close()
	})
	directory := auth.NewService(sqlDB, setupDir)
	if err := directory.RedeemSetup(context.Background(), code, "Ada Lovelace", "ada@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if err := runAdmin([]string{"setup-code"}); err == nil {
		t.Fatal("setup code issued after an account existed")
	}
	if err := runAdmin([]string{"setup-code", "--recover"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ReadSetupCode(setupDir); err != nil {
		t.Fatal(err)
	}
	if err := runAdmin([]string{"export"}); err == nil {
		t.Fatal("export without flags succeeded")
	}
	if err := runAdmin([]string{"purge", "--now"}); err == nil {
		t.Fatal("purge flag succeeded")
	}
	if err := runAdmin([]string{"export", "--household", "missing", "--out", filepath.Join(root, "missing.zip")}); err == nil {
		t.Fatal("missing household export succeeded")
	}

	var household string
	if err := sqlDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'primary_household_id'`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE households SET name = 'north-pantry-z9', channel_key = 'channel-sentinel-q7' WHERE id = ?`, household); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO products (id, name) VALUES ('p-north', 'north-catalog-q7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at) VALUES (?, 'p-north', 'north-typed-q7', '2026-01-01T00:00:00Z')`, household); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('i-north', 'acct', 'p-north', ?)`, household); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO household_password (household_id, pw_hash, state, session_key) VALUES (?, 'pw-hash-sentinel-q7', 'on', 'session-key-sentinel-q7')`, household); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	readDB.Close()

	out := filepath.Join(root, "exports", "north.zip")
	if err := runAdmin([]string{"export", "--household", household, "--out", out}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var doc string
	for _, file := range zr.File {
		names = append(names, file.Name)
		if file.Name == "stockbot-export.json" {
			rc, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			doc = string(body)
		}
	}
	if strings.Join(names, ",") != "stockbot-export.json,items.csv,shopping-list.csv" || !strings.Contains(doc, "north-typed-q7") || strings.Contains(doc, "channel-sentinel-q7") || strings.Contains(doc, "pw-hash-sentinel-q7") || strings.Contains(string(raw), "sbs_") || strings.Contains(string(raw), "sbd_") {
		t.Fatalf("cli zip names %v doc %s", names, doc)
	}

	exported, exportedReader, err := app.OpenPantry(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := exported.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'export.downloaded' AND actor_kind = 'admin' AND actor_id = 'cli' AND household_id = ?`, household).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("cli audit %d %v", audits, err)
	}
	if _, err := exported.Exec(`UPDATE households SET deleted_at = '2026-01-01T00:00:00Z', purge_after = '2026-01-02T00:00:00Z' WHERE id = ?`, household); err != nil {
		t.Fatal(err)
	}
	exported.Close()
	exportedReader.Close()
	if err := runAdmin([]string{"purge"}); err != nil {
		t.Fatal(err)
	}
	purged, purgedReader, err := app.OpenPantry(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer purged.Close()
	defer purgedReader.Close()
	var left int
	if err := purged.QueryRow(`SELECT COUNT(*) FROM households WHERE id = ?`, household).Scan(&left); err != nil || left != 0 {
		t.Fatalf("cli purge left %d %v", left, err)
	}
	var marker string
	if err := purged.QueryRow(`SELECT target_id FROM audit_log WHERE action = 'household.purged' AND household_id = ?`, household).Scan(&marker); err != nil || marker != household {
		t.Fatalf("cli purge audit %q %v", marker, err)
	}

	logSetupFile(filepath.Join(root, "absent"))
	bare := filepath.Join(root, "bare")
	if err := os.Mkdir(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "code"), []byte("Stockbot setup code: AAAA-AAAA\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	logSetupFile(bare)
}
