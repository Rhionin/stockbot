package app

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PreviewDirPrefix is the directory name prefix of a throwaway undo preview.
// The directory sits beside the pantry file. Startup removes leftovers.
const PreviewDirPrefix = ".stockbot-preview-"

// shoppingChangeMigration is the first migration that changes live shopping rows.
const shoppingChangeMigration = "012_shopping_list_group.sql"

// restockChangeMigration rebuilds the group rule check and adds no_restock.
const restockChangeMigration = "013_group_restock.sql"

// targetsChangeMigration copies legacy restock rows into item targets.
const targetsChangeMigration = "019_restock_targets.sql"

// shelveChangeMigration hides wiped items until the next stock-in.
// The column, the trigger, and the schema_migrations row commit together.
// A partial apply would leave the column behind, and the next startup
// would fail with "duplicate column name: shelved".
const shelveChangeMigration = "020_shelve_wiped_items.sql"

// backfillSourceMigration lets a history row record source backfill.
// SQLite cannot change a CHECK with ALTER TABLE, so the stock_moves table
// is rebuilt. The copy and the schema_migrations row commit together.
const backfillSourceMigration = "021_backfill_move_source.sql"

// backfillUnshelveMigration records that a backfill put a wiped item back.
// Undo of that pair hides the item again. The column and the
// schema_migrations row commit together.
const backfillUnshelveMigration = "022_backfill_unshelve.sql"

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
	// A killed run can leave a temp that holds credentials. It is not a
	// snapshot, and it should not still be on disk at the next startup.
	removeSnapshotTemps(dir)
	matches, err := filepath.Glob(filepath.Join(dir, spec.prefix+"-*.db"))
	if err != nil {
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	if len(matches) > 0 {
		log.Printf("%s: %s", spec.already, matches[0])
		return nil
	}
	// The temp name must not match prefix-*.db. A crash can leave the temp
	// behind, and the next run must still write a real snapshot.
	name := spec.prefix + "-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
	dest := filepath.Join(dir, name)
	temp := filepath.Join(dir, "."+name+".tmp")
	// VACUUM INTO creates the file itself. A private umask makes that
	// creation 0600, so a kill during the copy does not leave a
	// world-readable pantry. The chmod below covers a platform that
	// ignores umask.
	if err := withPrivateFiles(func() error {
		_, err := db.Exec(`VACUUM INTO ?`, temp)
		return err
	}); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	// The temp still has the mode it was created with. chmod has not run.
	if snapshotCopyHook != nil {
		if err := snapshotCopyHook(temp); err != nil {
			_ = os.Remove(temp)
			return fmt.Errorf("%s: %w", spec.fail, err)
		}
	}
	if err := syncFile(temp); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	// VACUUM INTO follows the process umask, which leaves the file readable
	// by other users. The snapshot is a full pantry copy.
	if err := os.Chmod(temp, 0o600); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	if err := os.Rename(temp, dest); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("%s: %w", spec.fail, err)
	}
	log.Printf("%s: %s", spec.wrote, dest)
	return nil
}

// snapshotCopyHook runs after VACUUM INTO and before the temp is chmodded.
// Tests use it to read the mode the copy itself created.
var snapshotCopyHook func(temp string) error

func removePreviewDirs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), PreviewDirPrefix) {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

func removeSnapshotTemps(dir string) {
	for _, prefix := range []string{"pantry-pre-groups", "pantry-pre-restock", "pantry-pre-targets"} {
		for _, pattern := range []string{
			"." + prefix + "-*.db.tmp",
			"." + prefix + "-*.db.tmp-journal",
		} {
			matches, err := filepath.Glob(filepath.Join(dir, pattern))
			if err != nil {
				continue
			}
			for _, match := range matches {
				_ = os.Remove(match)
			}
		}
	}
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	err = file.Sync()
	if err == nil || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func sqliteMainPath(db *sql.DB) (string, error) {
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return "", err
	}
	return file, nil
}
