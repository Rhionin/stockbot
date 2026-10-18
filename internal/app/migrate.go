// Package app provides application initialization and database setup.
package app

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// prepareMigrationSQL returns the SQL applied for a migration. Tests replace
// it to append a statement that fails partway through.
var prepareMigrationSQL = func(_ string, sqlText string) string { return sqlText }

// RunMigrations applies all numbered .sql files embedded in the migrations
// directory, in lexicographic order, skipping any that have already been
// recorded in the schema_migrations table. It is idempotent.
func RunMigrations(db *sql.DB) error {
	// Ensure the tracking table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	// Leftover snapshot temps can hold credentials from a killed migration.
	// Removing them does not depend on a migration still being pending.
	if path, err := sqliteMainPath(db); err == nil && path != "" && path != ":memory:" {
		removeSnapshotTemps(filepath.Dir(path))
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	// Collect .sql files; fs.ReadDir already returns entries sorted by name.
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	applied := 0
	for _, name := range files {
		// Check if already applied.
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, name).Scan(&count); err != nil {
			return fmt.Errorf("check migration %q: %w", name, err)
		}
		if count > 0 {
			continue
		}
		if name == shoppingChangeMigration {
			if err := snapshotShoppingDB(db); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
		}
		if name == restockChangeMigration {
			if err := snapshotRestockDB(db); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
		}
		if name == targetsChangeMigration {
			if err := snapshotTargetsDB(db); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
		}

		// Read the embedded SQL file.
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read embedded migration %q: %w", name, err)
		}
		sqlText := prepareMigrationSQL(name, string(sqlBytes))

		// 019 and 021 rebuild tables. 020 adds the shelved column and its trigger.
		// 022 adds the unshelved flag on a backfill stock-in.
		// VACUUM INTO cannot run inside a transaction, so the snapshot above
		// stays outside. Each of these files plus its schema_migrations row
		// commit together, so a failure leaves neither the schema change nor
		// a recorded migration.
		if name == targetsChangeMigration || name == shelveChangeMigration || name == backfillSourceMigration || name == backfillUnshelveMigration {
			if err := applyMigrationTx(db, name, sqlText); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
		} else if err := applyMigration(db, name, sqlText); err != nil {
			return fmt.Errorf("apply migration %q: %w", name, err)
		}

		log.Printf("applied migration: %s", name)
		applied++
	}

	if applied == 0 {
		log.Println("schema up to date, no migrations applied")
	}

	return nil
}

func applyMigration(db *sql.DB, name, sqlText string) error {
	if _, err := db.Exec(sqlText); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
		return err
	}
	return nil
}

func applyMigrationTx(db *sql.DB, name, sqlText string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(sqlText); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}
