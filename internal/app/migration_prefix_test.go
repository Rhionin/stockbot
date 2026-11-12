package app

import (
	"io/fs"
	"regexp"
	"testing"
)

// Three-digit migration stems must be unique (e.g. 030, 031). Letter-suffixed
// household steps (022a) and paired files (021_*) are out of scope here.
func TestMigrationThreeDigitPrefixUnique(t *testing.T) {
	seen := map[string]string{}
	re := regexp.MustCompile(`^(\d{3})_`)
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := re.FindStringSubmatch(name)
		if len(m) < 2 {
			continue
		}
		prefix := m[1]
		if prefix < "029" {
			continue
		}
		if prev, ok := seen[prefix]; ok {
			t.Fatalf("duplicate migration prefix %s: %q and %q", prefix, prev, name)
		}
		seen[prefix] = name
	}
}
