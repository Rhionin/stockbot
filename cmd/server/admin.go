package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

// runAdmin handles `stockbot-server admin setup-code [--recover]`.
// The display file is for the person at the Pi. This log records the path
// and when the code was issued, never the code itself.
func runAdmin(args []string) error {
	if len(args) == 0 || args[0] != "setup-code" {
		return fmt.Errorf("usage: stockbot-server admin setup-code [--recover]")
	}
	recover := false
	for _, arg := range args[1:] {
		if arg == "--recover" {
			recover = true
			continue
		}
		return fmt.Errorf("unknown argument %q", arg)
	}
	dbPath := envOrDefault("DB_PATH", "pantry.db")
	setupDir := envOrDefault("SETUP_DIR", "/run/setup")
	sqlDB, readDB, err := app.OpenPantry(dbPath)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	defer readDB.Close()
	if err := app.RunMigrations(sqlDB); err != nil {
		return err
	}
	directory := auth.NewService(sqlDB, setupDir)
	if err := directory.AdminIssue(context.Background(), recover); err != nil {
		return err
	}
	logSetupFile(setupDir)
	return nil
}

func logSetupFile(dir string) {
	path := filepath.Join(dir, "code")
	body, err := os.ReadFile(path)
	if err != nil {
		log.Printf("setup code file %s could not be read after it was written", path)
		return
	}
	issued := ""
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, "issued_at: "); ok {
			issued = strings.TrimSpace(rest)
		}
	}
	if issued == "" {
		log.Printf("setup code written to %s", path)
		return
	}
	log.Printf("setup code written to %s, issued %s, expires 24h after it was issued", path, issued)
}
