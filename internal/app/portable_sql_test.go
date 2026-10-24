package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pragmaAllowlist is infrastructure that has to read SQLite pragmas.
// Application SQL and the stage-1 migrations stay on the portable subset.
var pragmaAllowlist = map[string]string{
	"internal/app/backup.go":               "snapshot path comes from database_list",
	"internal/history/preview_snapshot.go": "undo preview path comes from database_list",
	"internal/backup/snapshot.go":          "backup integrity and page checks",
	"internal/grocysync/mapdb.go":          "grocy file busy timeout",
	"internal/grocysync/backfill.go":       "grocy stock_log shape and busy timeout",
	"internal/auth/purge.go":               "purge secure_delete, checkpoint, and table discovery",
}

func TestPortableSQL(t *testing.T) {
	root := moduleRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "frontend", "mobile", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		offenders = append(offenders, sqlViolations(t, path, "")...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "internal", "app", "migrations", "022[a-f]_*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 6 {
		t.Fatalf("household migrations: %v", matches)
	}
	later, err := filepath.Glob(filepath.Join(root, "internal", "app", "migrations", "02[3-9]_*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(later) < 2 {
		t.Fatalf("catalog and key migrations: %v", later)
	}
	matches = append(matches, later...)
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		offenders = append(offenders, sqlViolations(t, path, string(body))...)
	}
	if len(offenders) > 0 {
		t.Fatalf("SQL outside the portable subset:\n%s", strings.Join(offenders, "\n"))
	}
}

func sqlViolations(t *testing.T, path, text string) []string {
	t.Helper()
	if text == "" {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			out = append(out, violationsIn(path, value)...)
			return true
		})
		return out
	}
	return violationsIn(path, text)
}

func violationsIn(path, text string) []string {
	allowPragma := pragmaAllowed(path)
	var out []string
	for _, rule := range portableSQLRules {
		if allowPragma && rule.name == "PRAGMA" {
			continue
		}
		if rule.re.MatchString(text) {
			out = append(out, path+": "+rule.name)
		}
	}
	return out
}

func pragmaAllowed(path string) bool {
	slash := filepath.ToSlash(path)
	for rel := range pragmaAllowlist {
		if strings.HasSuffix(slash, rel) {
			return true
		}
	}
	return false
}

var portableSQLRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"INSERT OR REPLACE", regexp.MustCompile(`(?i)insert\s+or\s+replace`)},
	{"REPLACE INTO", regexp.MustCompile(`(?i)replace\s+into`)},
	{"datetime", regexp.MustCompile(`(?i)\bdatetime\s*\(`)},
	{"strftime", regexp.MustCompile(`(?i)\bstrftime\s*\(`)},
	{"printf", regexp.MustCompile(`(?i)\bprintf\s*\(`)},
	{"json", regexp.MustCompile(`(?i)\bjson_(extract|array|each|object|type|group)\s*\(`)},
	{"GLOB", regexp.MustCompile(`(?i)\bglob\b`)},
	{"CREATE TRIGGER", regexp.MustCompile(`(?i)\bcreate\s+trigger\b`)},
	{"AUTOINCREMENT", regexp.MustCompile(`(?i)\bautoincrement\b`)},
	{"INTEGER PRIMARY KEY", regexp.MustCompile(`(?i)\binteger\s+primary\s+key\b`)},
	{"PRAGMA", regexp.MustCompile(`(?i)\bpragma\b`)},
}
