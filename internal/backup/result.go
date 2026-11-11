package backup

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// BackupResult is the sanitized machine-readable outcome of one backup run.
// Field order matches the fixed JSON key set consumed by the host wrapper.
type BackupResult struct {
	Schema           int     `json:"schema"`
	OK               bool    `json:"ok"`
	ExitCode         int     `json:"exit_code"`
	ErrorClass       *string `json:"error_class"`
	Stored           bool    `json:"stored"`
	ObjectKey        *string `json:"object_key"`
	ManifestKey      *string `json:"manifest_key"`
	Integrity        *string `json:"integrity"`
	ForeignKeyCheck  *string `json:"foreign_key_check"`
	CiphertextSize   *int64  `json:"ciphertext_size"`
	CiphertextSHA256 *string `json:"ciphertext_sha256"`
	LastMigration    *string `json:"last_migration"`
	ToolVersion      *string `json:"tool_version"`
	Staging          *string `json:"staging"`
	CreatedAt        *string `json:"created_at"`
}

func backupResultFrom(err error, m Manifest) BackupResult {
	exit := ExitCode(err)
	if err == nil {
		exit = ExitOK
	}
	stored := m.ObjectKey != ""
	r := BackupResult{
		Schema:   1,
		OK:       err == nil,
		ExitCode: exit,
		Stored:   stored,
	}
	if err != nil {
		class := AlertClass(err)
		r.ErrorClass = &class
	}
	if stored {
		r.ObjectKey = &m.ObjectKey
		if mkey, merr := manifestKeyFor(m.ObjectKey); merr == nil {
			r.ManifestKey = &mkey
		}
		r.Integrity = &m.Integrity
		r.ForeignKeyCheck = &m.ForeignKeyCheck
		size := m.CiphertextSize
		r.CiphertextSize = &size
		r.CiphertextSHA256 = &m.CiphertextSHA256
		r.LastMigration = &m.LastMigration
		r.ToolVersion = &m.ToolVersion
		r.Staging = &m.Staging
		r.CreatedAt = &m.CreatedAt
	}
	return r
}

func printBackupResult(err error, m Manifest) {
	r := backupResultFrom(err, m)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return
	}
	data := bytes.TrimSpace(buf.Bytes())
	fmt.Printf("backup result %s\n", data)
}
