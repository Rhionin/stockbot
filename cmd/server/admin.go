package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/export"
)

// runAdmin handles setup codes, household export, and the purge job.
// The setup display file is for the person at the Pi. This log records the
// path and when the code was issued, never the code itself.
func runAdmin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: stockbot-server admin setup-code [--recover] | export --household <id> --out <path> | purge")
	}
	switch args[0] {
	case "setup-code":
		return runSetupCode(args[1:])
	case "export":
		return runExport(args[1:])
	case "purge":
		return runPurge(args[1:])
	default:
		return fmt.Errorf("usage: stockbot-server admin setup-code [--recover] | export --household <id> --out <path> | purge")
	}
}

func runSetupCode(args []string) error {
	recover := false
	for _, arg := range args {
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

func runExport(args []string) error {
	household := ""
	out := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--household":
			if i+1 >= len(args) {
				return fmt.Errorf("export: --household needs an id")
			}
			i++
			household = args[i]
		case "--out":
			if i+1 >= len(args) {
				return fmt.Errorf("export: --out needs a path")
			}
			i++
			out = args[i]
		default:
			return fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if household == "" || out == "" {
		return fmt.Errorf("usage: stockbot-server admin export --household <id> --out <path>")
	}
	sqlDB, readDB, err := app.OpenPantry(envOrDefault("DB_PATH", "pantry.db"))
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	defer readDB.Close()
	if err := app.RunMigrations(sqlDB); err != nil {
		return err
	}
	bundle, err := export.Build(context.Background(), sqlDB, household, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil && filepath.Dir(out) != "." {
		return err
	}
	if err := os.WriteFile(out, bundle.Zip, 0o600); err != nil {
		return err
	}
	directory := auth.NewService(sqlDB, envOrDefault("SETUP_DIR", "/run/setup"))
	return directory.RecordExport(context.Background(), "admin", "cli", household)
}

func runPurge(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: stockbot-server admin purge")
	}
	sqlDB, readDB, err := app.OpenPantry(envOrDefault("DB_PATH", "pantry.db"))
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	defer readDB.Close()
	if err := app.RunMigrations(sqlDB); err != nil {
		return err
	}
	directory := auth.NewService(sqlDB, envOrDefault("SETUP_DIR", "/run/setup"))
	result, err := directory.PurgeDue(context.Background())
	if err != nil {
		return err
	}
	log.Printf("purge: households %d, passwords %d", result.Households, result.Passwords)
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
