package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/tenant"
	"modernc.org/sqlite"
)

// previewReserveFloor is the free space a preview leaves on the data volume
// after the copy, when the database itself is smaller than that.
const previewReserveFloor uint64 = 32 << 20

var errFreeSpace = errors.New("free space is unavailable")

// PreviewFullError is a preview copy that could not be written because the
// disk is full. The HTTP layer answers 503 and keeps this error in the log.
type PreviewFullError struct {
	Err error
}

func (e *PreviewFullError) Error() string {
	if e == nil || e.Err == nil {
		return "disk full"
	}
	return e.Err.Error()
}

func (e *PreviewFullError) Unwrap() error { return e.Err }

// One preview at a time. Each copy is about the size of the pantry, and the
// data volume has to hold it. A waiter stops when the request is canceled.
var previewSlots = make(chan struct{}, 1)

// previewBackup copies the open pantry to dest. Tests replace it.
var previewBackup = backupDatabase

// previewOpen opens a preview copy. Tests replace it.
var previewOpen = app.OpenSQLite

// previewDuringBackup runs after the first page of a copy, before the backup
// transaction finishes. Tests read the file mode there.
var previewDuringBackup func(path string) error

// previewEachStep runs after every backup step. Tests cancel between pages.
var previewEachStep func() error

// previewBackupPages is how many pages one step copies after the first page.
// The first page is copied alone so the journal exists for the mode check.
// A step never copies the rest of the database in one call, so a canceled
// preview can release its slot before the copy finishes.
var previewBackupPages int32 = 32

// previewFree reports free bytes on the volume that holds path. Tests replace it.
var previewFree = statfsFree

// SetPreviewBackupForTest replaces the preview copy until restore runs.
func SetPreviewBackupForTest(fn func(ctx context.Context, src *sql.DB, dest string) error) func() {
	previous := previewBackup
	if fn == nil {
		previewBackup = backupDatabase
	} else {
		previewBackup = fn
	}
	return func() { previewBackup = previous }
}

func acquirePreview(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case previewSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-previewSlots
			return nil, err
		}
		return func() { <-previewSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// snapshot copies the pantry into a private database beside the pantry file.
// Preview runs the real undo there. The pantry connection is only read, so
// the copy does not take its write lock and the history_repair update never
// lands on it. One preview holds the slot until the copy is removed.
func (j *Journal) snapshot(ctx context.Context) (*sql.DB, string, func(), error) {
	parent, file, err := previewLocation(ctx, j.db)
	if err != nil {
		return nil, "", nil, previewErr(ctx, err)
	}
	need, err := previewSpaceNeed(file)
	if err != nil {
		return nil, "", nil, previewErr(ctx, err)
	}
	free, err := previewFree(parent)
	if err != nil {
		return nil, "", nil, previewErr(ctx, err)
	}
	if free < need {
		return nil, "", nil, &PreviewFullError{Err: fmt.Errorf("not enough free space")}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", nil, err
	}
	dir, err := makePreviewDir(parent)
	if err != nil {
		return nil, "", nil, previewErr(ctx, err)
	}
	unlock, err := app.HoldPreviewDir(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", nil, previewErr(ctx, err)
	}
	cleanupDir := func() {
		unlock()
		_ = os.RemoveAll(dir)
	}
	path := filepath.Join(dir, "preview.db")
	dbFile, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanupDir()
		return nil, "", nil, previewErr(ctx, err)
	}
	dbFile.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		cleanupDir()
		return nil, "", nil, previewErr(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		cleanupDir()
		return nil, "", nil, err
	}
	if err := previewBackup(ctx, j.db, path); err != nil {
		cleanupDir()
		return nil, "", nil, previewErr(ctx, err)
	}
	if err := chmodPreview(dir); err != nil {
		cleanupDir()
		return nil, "", nil, previewErr(ctx, err)
	}
	clone, err := previewOpen(path)
	if err != nil {
		cleanupDir()
		return nil, "", nil, previewErr(ctx, err)
	}
	clone.SetMaxOpenConns(1)
	return clone, dir, func() {
		clone.Close()
		cleanupDir()
	}, nil
}

func previewLocation(ctx context.Context, db *sql.DB) (parent, file string, err error) {
	var seq int
	var name string
	err = db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &file)
	if err != nil {
		return "", "", err
	}
	// An in-memory database has no data directory. Production opens a file,
	// and the copy goes next to that file rather than on the small /tmp.
	if file == "" || file == ":memory:" {
		return os.TempDir(), file, nil
	}
	return filepath.Dir(file), file, nil
}

// previewSpaceNeed is the free space a copy must leave untouched: the
// database, its journal, and a reserve. The reserve is the larger of 32 MiB
// and the database file, so a copy cannot consume the last of the volume.
func previewSpaceNeed(file string) (uint64, error) {
	if file == "" || file == ":memory:" {
		return previewReserveFloor, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return 0, err
	}
	dbSize := uint64(info.Size())
	var journal uint64
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		side, err := os.Stat(file + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		if side.Size() > 0 {
			journal += uint64(side.Size())
		}
	}
	return dbSize + journal + previewReserve(dbSize), nil
}

func previewReserve(dbSize uint64) uint64 {
	if dbSize > previewReserveFloor {
		return dbSize
	}
	return previewReserveFloor
}

func makePreviewDir(parent string) (string, error) {
	for n := 0; n < 100; n++ {
		name := app.PreviewDirPrefix + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.Itoa(n)
		dir := filepath.Join(parent, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		return dir, nil
	}
	return "", fmt.Errorf("could not create a preview directory")
}

func backupDatabase(ctx context.Context, src *sql.DB, dest string) error {
	conn, err := src.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		backer, ok := driverConn.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("backup is not available")
		}
		backup, err := backer.NewBackup(dest)
		if err != nil {
			return err
		}
		pages := int32(1)
		hooked := false
		for {
			if err := ctx.Err(); err != nil {
				_ = backup.Finish()
				return err
			}
			more, err := backup.Step(pages)
			if err != nil {
				_ = backup.Finish()
				return err
			}
			if err := chmodBackupFiles(dest); err != nil {
				_ = backup.Finish()
				return err
			}
			if !hooked {
				hooked = true
				pages = previewBackupPages
				if previewDuringBackup != nil {
					if err := previewDuringBackup(dest); err != nil {
						_ = backup.Finish()
						return err
					}
				}
			}
			if previewEachStep != nil {
				if err := previewEachStep(); err != nil {
					_ = backup.Finish()
					return err
				}
			}
			if !more {
				break
			}
		}
		return backup.Finish()
	})
}

func chmodBackupFiles(dest string) error {
	for _, path := range []string{dest, dest + "-journal", dest + "-wal", dest + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		mode := os.FileMode(0o600)
		if info.IsDir() {
			mode = 0o700
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func chmodPreview(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		return os.Chmod(path, mode)
	})
}

func previewErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var missing *NotFoundError
	var input *InputError
	var conflict *ConflictError
	var full *PreviewFullError
	if errors.As(err, &missing) || errors.As(err, &input) || errors.As(err, &conflict) || errors.As(err, &full) || errors.Is(err, ErrBackfillLocked) || errors.Is(err, tenant.ErrMissingHousehold) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx.Err() != nil && (strings.Contains(strings.ToLower(err.Error()), "interrupted") || strings.Contains(strings.ToLower(err.Error()), "context canceled")) {
		return ctx.Err()
	}
	if diskFull(err) {
		return &PreviewFullError{Err: err}
	}
	return fmt.Errorf("read the shelf: %w", err)
}

func diskFull(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) && driverErr.Code()&0xff == 13 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "disk is full") || strings.Contains(msg, "database is full") || strings.Contains(msg, "no space left")
}
