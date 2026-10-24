package main

import (
	"context"
	"os"
	"path/filepath"
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
