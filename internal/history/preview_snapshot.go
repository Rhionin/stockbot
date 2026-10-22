package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Rhionin/stockbot/internal/app"
	"modernc.org/sqlite"
)

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
func (j *Journal) snapshot(ctx context.Context) (*sql.DB, func(), error) {
	parent, err := previewParent(ctx, j.db)
	if err != nil {
		return nil, nil, previewErr(ctx, err)
	}
	var dir string
	err = app.WithPrivateFiles(func() error {
		var mkdirErr error
		dir, mkdirErr = os.MkdirTemp(parent, app.PreviewDirPrefix)
		if mkdirErr != nil {
			return mkdirErr
		}
		path := filepath.Join(dir, "preview.db")
		if err := previewBackup(ctx, j.db, path); err != nil {
			return err
		}
		return chmodPreview(dir)
	})
	if err != nil {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		return nil, nil, previewErr(ctx, err)
	}
	path := filepath.Join(dir, "preview.db")
	clone, err := previewOpen(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, previewErr(ctx, err)
	}
	clone.SetMaxOpenConns(1)
	return clone, func() {
		clone.Close()
		_ = os.RemoveAll(dir)
	}, nil
}

func previewParent(ctx context.Context, db *sql.DB) (string, error) {
	var seq int
	var name, file string
	err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &file)
	if err != nil {
		return "", err
	}
	// An in-memory database has no data directory. Production opens a file,
	// and the copy goes next to that file rather than on the small /tmp.
	if file == "" || file == ":memory:" {
		return os.TempDir(), nil
	}
	return filepath.Dir(file), nil
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
		stepped := false
		for {
			if err := ctx.Err(); err != nil {
				_ = backup.Finish()
				return err
			}
			more, err := backup.Step(1)
			if err != nil {
				_ = backup.Finish()
				return err
			}
			if !stepped {
				stepped = true
				if previewDuringBackup != nil {
					if err := previewDuringBackup(dest); err != nil {
						_ = backup.Finish()
						return err
					}
				}
			}
			if !more {
				break
			}
			more, err = backup.Step(-1)
			if err != nil {
				_ = backup.Finish()
				return err
			}
			if !more {
				break
			}
		}
		return backup.Finish()
	})
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
	if errors.As(err, &missing) || errors.As(err, &input) || errors.As(err, &conflict) || errors.As(err, &full) || errors.Is(err, ErrBackfillLocked) {
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
