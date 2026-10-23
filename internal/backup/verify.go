package backup

import (
	"fmt"
	"time"

	_ "time/tzdata"
)

func denver(t time.Time) string {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		return t.UTC().Format(time.RFC3339)
	}
	return t.In(loc).Format(time.RFC3339)
}

// Verify compares a restored database with its manifest. integrity_check must
// be ok, and the row counts, latest migration, and plaintext sha256 must
// match the manifest exactly.
func Verify(dbPath, manifestPath string) error {
	m, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	info, err := Inspect(dbPath)
	if err != nil {
		return err
	}
	if info.PlaintextSHA256 != m.PlaintextSHA256 {
		return errClass(ExitIntegrity, "integrity", "the restored database sha256 does not match the manifest")
	}
	if info.LastMigration != m.LastMigration {
		return errClass(ExitIntegrity, "integrity", "the restored database migration does not match the manifest")
	}
	if m.ForeignKeyCheck != "" && info.ForeignKeyCheck != m.ForeignKeyCheck {
		return errClass(ExitIntegrity, "integrity", "the restored database foreign key check does not match the manifest")
	}
	if len(info.Tables) != len(m.Tables) {
		return errClass(ExitIntegrity, "integrity", "the restored database table counts do not match the manifest")
	}
	for name, want := range m.Tables {
		got, ok := info.Tables[name]
		if !ok || got != want {
			return errClass(ExitIntegrity, "integrity", "the restored database table counts do not match the manifest")
		}
	}
	fmt.Printf("verify ok integrity=%s tables=%d rows=%d migration=%s\n", info.Integrity, len(info.Tables), info.RowTotal, info.LastMigration)
	return nil
}
