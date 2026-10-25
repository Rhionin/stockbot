package hub

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State is the hub's credentials. The file is mode 0600 because token is a
// bearer credential returned only once.
type State struct {
	ServerURL string         `json:"serverUrl"`
	DeviceID  string         `json:"deviceId,omitempty"`
	Token     string         `json:"token,omitempty"`
	Name      string         `json:"name,omitempty"`
	BootID    string         `json:"bootId,omitempty"`
	Rollback  bool           `json:"rollback,omitempty"`
	Pending   *PendingUpdate `json:"pendingUpdate,omitempty"`
}

// PendingUpdate is an activated release that still has to prove itself.
// Deadline is 120 seconds after the symlink switches.
type PendingUpdate struct {
	Version         string    `json:"version"`
	PreviousVersion string    `json:"previousVersion,omitempty"`
	PreviousPath    string    `json:"previousPath,omitempty"`
	Binary          string    `json:"binary,omitempty"`
	Deadline        time.Time `json:"deadline"`
}

func loadState(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	if len(raw) == 0 {
		return State{}, nil
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		// The file holds the bearer token. The error must not quote it.
		return State{}, errors.New("the hub credentials file is unreadable")
	}
	return state, nil
}

func saveState(path string, state State) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return writePrivate(path, raw)
}

// writePrivate replaces path with data, fsyncs it, and leaves it mode 0600.
// The token is written and flushed before the hub uses a rotated credential.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cred-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	keep = true
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
