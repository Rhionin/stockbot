package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWithUTCTimezone(t *testing.T) {
	if got := withUTCTimezone(":memory:"); got != ":memory:?_timezone=UTC" {
		t.Fatalf("memory: %s", got)
	}
	if got := withUTCTimezone("file:pantry.db?_pragma=busy_timeout(5000)"); got != "file:pantry.db?_pragma=busy_timeout(5000)&_timezone=UTC" {
		t.Fatalf("pragma: %s", got)
	}
	if got := withUTCTimezone("pantry.db?_timezone=America/Denver&_txlock=immediate"); got != "pantry.db?_timezone=UTC&_txlock=immediate" {
		t.Fatalf("replace: %s", got)
	}
	if got := withUTCTimezone("pantry.db?_txlock=immediate&_timezone=Local"); got != "pantry.db?_txlock=immediate&_timezone=UTC" {
		t.Fatalf("replace tail: %s", got)
	}
}

func TestOpenSQLiteStoresDenverTimesAsUTC(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE stamps (id TEXT, at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(denver)
	fixed := time.Date(2026, 11, 1, 0, 30, 0, 0, denver)
	// time.Now still carries a monotonic reading. CAST keeps the stored text;
	// a plain SELECT into a string is reformatted by the driver and hides " m=+".
	if _, err := db.Exec(`INSERT INTO stamps (id, at) VALUES ('now', ?), ('fixed', ?), ('mono', ?)`, now, fixed, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id, CAST(at AS TEXT) FROM stamps ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "MDT") || strings.Contains(raw, "MST") || strings.Contains(raw, "m=") {
			t.Fatalf("%s stored %s", id, raw)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// A raw sqlite open outside OpenSQLite skips _timezone=UTC, so a Denver
// time.Time can reach SQL again. The needle is built at runtime so this
// file does not contain the call it forbids.
func TestSQLiteOpensGoThroughUTC(t *testing.T) {
	root := moduleRoot(t)
	needle := "sql.Open(" + `"sqlite"`
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "frontend", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "sqlite.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), needle) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("open the database with app.OpenSQLite so timestamps are UTC:\n%s", strings.Join(offenders, "\n"))
	}
	// The walk above is text. Parse OpenSQLite itself so a renamed call still
	// has to be the one sql.Open in that file.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, "internal", "app", "sqlite.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	opens := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Open" {
			return true
		}
		opens++
		return true
	})
	if opens != 1 {
		t.Fatalf("sqlite.go has %d Open calls, want 1", opens)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
