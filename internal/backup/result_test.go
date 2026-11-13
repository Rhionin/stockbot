package backup

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestBackupResultFromFailure(t *testing.T) {
	r := backupResultFrom(errClass(ExitUpload, "upload", "boom"), Manifest{})
	if r.OK || r.Stored || r.ExitCode != ExitUpload {
		t.Fatalf("failure result: %#v", r)
	}
	if r.ErrorClass == nil || *r.ErrorClass != "upload" {
		t.Fatalf("error_class: %#v", r.ErrorClass)
	}
}

func TestBackupResultFromStoredOmitsManifestOnBadObjectKey(t *testing.T) {
	m := Manifest{ObjectKey: "not-a-pantry-snapshot"}
	r := backupResultFrom(nil, m)
	if !r.OK || !r.Stored {
		t.Fatalf("stored ok: %#v", r)
	}
	if r.ObjectKey == nil || *r.ObjectKey != m.ObjectKey {
		t.Fatalf("object_key: %#v", r.ObjectKey)
	}
	if r.ManifestKey != nil {
		t.Fatalf("manifest_key should be omitted for invalid object key: %#v", r.ManifestKey)
	}
}

func TestBackupResultFromStoredIncludesManifestKey(t *testing.T) {
	key := "backups/2026-01-01T120000Z.pantry.db.gz.age"
	m := Manifest{
		ObjectKey:        key,
		Integrity:        "ok",
		ForeignKeyCheck:  "ok",
		CiphertextSize:   42,
		CiphertextSHA256: "abc",
		LastMigration:    "031_trip_scoped_skips",
		ToolVersion:      "test",
		Staging:          "tmpfs",
		CreatedAt:        "2026-01-01T12:00:00Z",
	}
	r := backupResultFrom(nil, m)
	if r.ManifestKey == nil || *r.ManifestKey != "backups/2026-01-01T120000Z.manifest.json" {
		t.Fatalf("manifest_key: %#v", r.ManifestKey)
	}
}

func TestPrintBackupResultEmitsJSONLine(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printBackupResult(nil, Manifest{ObjectKey: "x/2026.pantry.db.gz.age", Integrity: "ok"})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("backup result ")) {
		t.Fatalf("stdout: %q", out)
	}
	var payload BackupResult
	if err := json.Unmarshal(bytes.TrimSpace(bytes.TrimPrefix(out, []byte("backup result "))), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Schema != 1 {
		t.Fatalf("payload: %#v", payload)
	}
}
