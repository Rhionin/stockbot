package backup

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultStageDir is the RAM-backed staging directory. It is a tmpfs
	// on the Pi (/run is tmpfs), mounted into the backup container.
	DefaultStageDir = "/run/stockbot-backup"

	// DefaultTmpfsLimit is the tmpfs size mounted at DefaultStageDir.
	DefaultTmpfsLimit int64 = 64 << 20

	// stageHeadroomNum/stageHeadroomDen is the room a snapshot needs:
	// the database plus half again, so two and a half times the file.
	stageHeadroomNum int64 = 5
	stageHeadroomDen int64 = 2

	volumeStageDir = ".backup-staging"
)

// StagePlan is where the plaintext snapshot is written.
type StagePlan struct {
	Dir       string
	Kind      string // "tmpfs" or "volume"
	RemoveDir bool
}

// StageNeed is the bytes the snapshot needs for a database of dbBytes,
// including headroom for the copy.
func StageNeed(dbBytes int64) int64 {
	if dbBytes < 0 {
		dbBytes = 0
	}
	return dbBytes * stageHeadroomNum / stageHeadroomDen
}

// ChooseStage uses the tmpfs directory when the database plus headroom fits
// in the tmpfs limit. Otherwise it uses a directory next to the database,
// on the data volume. It does not refuse because of size.
func ChooseStage(dbPath, tmpfsDir string, dbBytes, tmpfsLimit int64) StagePlan {
	if tmpfsDir != "" && StageNeed(dbBytes) <= tmpfsLimit {
		return StagePlan{Dir: tmpfsDir, Kind: "tmpfs"}
	}
	return StagePlan{
		Dir:       filepath.Join(filepath.Dir(dbPath), volumeStageDir),
		Kind:      "volume",
		RemoveDir: true,
	}
}

// Cleanup removes snapshot files. The volume directory itself is removed.
// The tmpfs mount is left in place.
func (p StagePlan) Cleanup() {
	if p.Dir == "" {
		return
	}
	entries, err := os.ReadDir(p.Dir)
	if err == nil {
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(p.Dir, entry.Name()))
		}
	}
	if p.RemoveDir {
		_ = os.Remove(p.Dir)
	}
}

// CleanupStage removes a leftover data-volume staging directory for dbPath.
func CleanupStage(dbPath string) error {
	if dbPath == "" {
		return errClass(ExitPreflight, "preflight", "the database path is missing")
	}
	dir := filepath.Join(filepath.Dir(dbPath), volumeStageDir)
	if err := os.RemoveAll(dir); err != nil {
		return errWrap(ExitPreflight, "preflight", "could not remove the backup staging directory", err)
	}
	return nil
}

func databaseBytes(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, errClass(ExitPreflight, "preflight", "could not read the pantry database")
	}
	if fi.IsDir() {
		return 0, errClass(ExitPreflight, "preflight", "the pantry database path is a directory")
	}
	n := fi.Size()
	wal, err := os.Stat(path + "-wal")
	if err == nil && !wal.IsDir() {
		n += wal.Size()
	} else if err != nil && !os.IsNotExist(err) {
		return 0, errWrap(ExitPreflight, "preflight", "could not read the pantry database", err)
	}
	return n, nil
}

func resolveTmpfsLimit(dir string, override int64) int64 {
	if override > 0 {
		return override
	}
	if dir != "" {
		if n, err := filesystemSize(dir); err == nil && n > 0 {
			return n
		}
	}
	return DefaultTmpfsLimit
}

func prepareStage(plan StagePlan) error {
	if err := os.MkdirAll(plan.Dir, 0o700); err != nil {
		return errWrap(ExitPreflight, "preflight", "could not create the backup staging directory", err)
	}
	if err := os.Chmod(plan.Dir, 0o700); err != nil {
		return errWrap(ExitPreflight, "preflight", "could not protect the backup staging directory", err)
	}
	return nil
}

func stageLog(plan StagePlan) string {
	return fmt.Sprintf("backup staging: %s %s", plan.Kind, plan.Dir)
}
