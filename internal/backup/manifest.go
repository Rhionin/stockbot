package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
)

// Manifest is the unencrypted object stored next to a backup. It holds
// counts, ids, hashes, and sizes. It has no names, barcodes, or row contents.
type Manifest struct {
	ObjectKey        string           `json:"object_key"`
	CiphertextSHA256 string           `json:"ciphertext_sha256"`
	CiphertextSize   int64            `json:"ciphertext_size"`
	PlaintextSHA256  string           `json:"plaintext_sha256"`
	PlaintextSize    int64            `json:"plaintext_size"`
	Integrity        string           `json:"integrity"`
	ForeignKeyCheck  string           `json:"foreign_key_check"`
	Tables           map[string]int64 `json:"tables"`
	LastMigration    string           `json:"last_migration"`
	ToolVersion      string           `json:"tool_version"`
	ImageDigest      string           `json:"image_digest"`
	DurationMS       int64            `json:"duration_ms"`
	Reason           string           `json:"reason"`
	CreatedAt        string           `json:"created_at"`
	PageCount        int64            `json:"page_count"`
	PageSize         int64            `json:"page_size"`
	RowTotal         int64            `json:"row_total"`
	Staging          string           `json:"staging"`
}

// SecretsManifest describes an encrypted secrets archive. Paths and file
// contents stay inside the ciphertext.
type SecretsManifest struct {
	ObjectKey        string `json:"object_key"`
	CiphertextSHA256 string `json:"ciphertext_sha256"`
	CiphertextSize   int64  `json:"ciphertext_size"`
	FileCount        int    `json:"file_count"`
	ToolVersion      string `json:"tool_version"`
	CreatedAt        string `json:"created_at"`
}

func objectNames(now time.Time, reason string) (dataKey, manifestKey string, err error) {
	ts := now.UTC().Format("2006-01-02T150405Z")
	switch reason {
	case "nightly":
		base := "nightly/" + ts
		return base + ".pantry.db.gz.age", base + ".manifest.json", nil
	case "manual", "pre-deploy", "drill":
		base := "manual/" + ts + "." + reason
		return base + ".pantry.db.gz.age", base + ".manifest.json", nil
	default:
		return "", "", errClass(ExitPreflight, "preflight", "the backup reason must be nightly, manual, pre-deploy, or drill")
	}
}

func secretsObjectNames(now time.Time) (dataKey, manifestKey string) {
	ts := now.UTC().Format("2006-01-02T150405Z")
	base := "secrets/" + ts
	return base + ".tar.age", base + ".manifest.json"
}

func manifestKeyFor(objectKey string) (string, error) {
	const suffix = ".pantry.db.gz.age"
	if len(objectKey) <= len(suffix) || objectKey[len(objectKey)-len(suffix):] != suffix {
		return "", errClass(ExitPreflight, "preflight", "the backup object name is not a pantry snapshot")
	}
	return objectKey[:len(objectKey)-len(suffix)] + ".manifest.json", nil
}

func (m Manifest) marshal() ([]byte, error) {
	if m.Tables == nil {
		m.Tables = map[string]int64{}
	}
	if m.ToolVersion == "" {
		m.ToolVersion = buildinfo.VisibleVersion()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, errWrap(ExitUpload, "upload", "could not write the backup manifest", err)
	}
	return buf.Bytes(), nil
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, errWrap(ExitPreflight, "preflight", "could not read the backup manifest", err)
	}
	return parseManifest(data)
}

func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, errWrap(ExitPreflight, "preflight", "the backup manifest is not valid", err)
	}
	return m, nil
}
