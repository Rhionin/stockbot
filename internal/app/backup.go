package app

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"time"
)

// shoppingChangeMigration is the first migration that changes live shopping rows.
const shoppingChangeMigration = "012_shopping_list_group.sql"

// restockChangeMigration rebuilds the group rule check and adds no_restock.
const restockChangeMigration = "013_group_restock.sql"

// targetsChangeMigration copies legacy restock rows into item targets.
const targetsChangeMigration = "019_restock_targets.sql"

// snapshotShoppingDB runs once, immediately before shoppingChangeMigration.
// Tests replace it to simulate a backup that cannot be written.
var snapshotShoppingDB = writePreGroupsBackup

// snapshotRestockDB runs once, immediately before restockChangeMigration.
// Tests replace it to simulate a backup that cannot be written.
var snapshotRestockDB = writePreRestockBackup

// snapshotTargetsDB runs once, immediately before targetsChangeMigration.
// Tests replace it to simulate a backup that cannot be written.
var snapshotTargetsDB = writePreTargetsBackup

type snapshotSpec struct {
	prefix  string
	already string
	wrote   string
	fail    string
}

func writePreGroupsBackup(db *sql.DB) error {
	return writeSnapshot(db, snapshotSpec{
		prefix:  "pantry-pre-groups",
		already: "shopping backup already exists",
		wrote:   "wrote shopping backup",
		fail:    "could not back up the pantry before updating shopping",
	})
}

func writePreRestockBackup(db *sql.DB) error {
	return writeSnapshot(db, snapshotSpec{
		prefix:  "pantry-pre-restock",
		already: "restock backup already exists",
		wrote:   "wrote restock backup",
		fail:    "could not back up the pantry before updating restock rules",
	})
}

func writePreTargetsBackup(db *sql.DB) error {
	return writeSnapshot(db, snapshotSpec{
		prefix:  "pantry-pre-targets",
		already: "restock targets backup already exists",
		wrote:   "wrote restock targets backup",
		fail:    "could not back up the pantry before updating restock targets",
	})
}

func writeSnapshot(db *sql.DB, spec snapshotSpec) error {
	path, err := sqliteMainPath(db)
	if err != nil {
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	if path == "" || path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	matches, err := filepath.Glob(filepath.Join(dir, spec.prefix+"-*.db"))
	if err != nil {
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	if len(matches) > 0 {
		log.Printf("%s: %s", spec.already, matches[0])
		return nil
	}
	dest := filepath.Join(dir, spec.prefix+"-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	if _, err := db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	log.Printf("%s: %s", spec.wrote, dest)
	return nil
}

func sqliteMainPath(db *sql.DB) (string, error) {
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return "", err
	}
	return file, nil
}
