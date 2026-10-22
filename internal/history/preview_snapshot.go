package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rhionin/stockbot/internal/app"
	"modernc.org/sqlite"
)

// snapshot copies the pantry into a private database. Preview runs the real
// undo there. The pantry connection is only read, so the copy does not take
// its write lock and the history_repair update never lands on it.
func (j *Journal) snapshot(ctx context.Context) (*sql.DB, func(), error) {
	dir, err := os.MkdirTemp("", "stockbot-preview-")
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "preview.db")
	fail := func(err error) (*sql.DB, func(), error) {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	conn, err := j.db.Conn(ctx)
	if err != nil {
		return fail(fmt.Errorf("read the shelf: %w", err))
	}
	defer conn.Close()
	if err := conn.Raw(func(driverConn any) error {
		backer, ok := driverConn.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("read the shelf: backup is not available")
		}
		backup, err := backer.NewBackup(path)
		if err != nil {
			return err
		}
		more := true
		for more {
			more, err = backup.Step(-1)
			if err != nil {
				_ = backup.Finish()
				return err
			}
		}
		return backup.Finish()
	}); err != nil {
		return fail(fmt.Errorf("read the shelf: %w", err))
	}
	clone, err := app.OpenSQLite(path)
	if err != nil {
		return fail(err)
	}
	clone.SetMaxOpenConns(1)
	return clone, func() {
		clone.Close()
		os.RemoveAll(dir)
	}, nil
}
