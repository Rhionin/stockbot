package tenant

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// HouseholdTables are rows one household must not see from another.
// Global catalog tables are not in this list. Schema migrations are not scanned.
var HouseholdTables = []string{
	"audit_log",
	"barcodes",
	"brand_preferences",
	"cart_integrations",
	"categories",
	"category_suggestion_dismissals",
	"category_targets",
	"change_versions",
	"consumption_events",
	"device_tokens",
	"devices",
	"fulfillment_ledger",
	"group_suggestion_dismissals",
	"group_suggestion_members",
	"group_suggestions",
	"history_repair",
	"household_password",
	"household_product_overrides",
	"household_settings",
	"invites",
	"item_categories",
	"item_deals",
	"item_instances",
	"item_targets",
	"items",
	"memberships",
	"pairing_codes",
	"product_contributions",
	"product_group_members",
	"product_groups",
	"provider_app_credentials",
	"provider_connections",
	"push_subscriptions",
	"scan_alert_schedule",
	"scan_entries",
	"shopping_list_entry_adjustments",
	"shopping_list_items",
	"staged_cart_skips",
	"stock_in_events",
	"stock_moves",
	"supply_overrides",
}

// GlobalTables are shared on purpose. A statement that only touches these
// tables does not need a household predicate.
var GlobalTables = map[string]string{
	"products":           "public catalog lookup result",
	"product_off_tags":   "public catalog tags",
	"barcode_misses":     "public lookup misses, with no household",
	"households":         "household directory",
	"users":              "account directory",
	"app_settings":       "server settings",
	"setup_codes":        "server setup codes",
	"schema_migrations":  "schema history",
	"sqlite_master":      "sqlite catalog",
	"sqlite_temp_master": "sqlite catalog",
	"sqlite_sequence":    "sqlite catalog",
}

// sqlAllow is a household statement that cannot bind household_id.
// The reason is the only thing that keeps it out of the lint.
var sqlAllow = []struct {
	file    string
	snippet string
	reason  string
}{
	{
		file:    "internal/app/household.go",
		snippet: "NOT IN",
		reason:  "pre-migration guard runs before household_id exists",
	},
	{
		file:    "internal/auth/scope.go",
		snippet: "d.kind = 'direct'",
		reason:  "resolves the seeded direct scanner; household id is the result",
	},
}

var (
	tableRef   = regexp.MustCompile(`(?i)\b(?:from|join|update|into|table)\s+([a-z_][a-z0-9_]*)`)
	scopedPred = regexp.MustCompile(`(?i)(?:\b[a-z_][a-z0-9_]*\.)?household_id\s*=\s*\?`)
	insertHead = regexp.MustCompile(`(?i)^\s*insert\s+into\s+([a-z_][a-z0-9_]*)\s*\(([^)]*)\)`)
	sqlStart   = regexp.MustCompile(`(?i)\b(select|insert|update|delete|with)\b`)
)

// CheckStatement reports household tables this statement touches without a
// bound household_id. An insert may name household_id in its column list.
// A statement that only touches the global catalog is allowed.
func CheckStatement(sqlText string) []string {
	return checkStatement(sqlText, "")
}

func checkStatement(sqlText, file string) []string {
	if strings.TrimSpace(sqlText) == "" || !sqlStart.MatchString(sqlText) {
		return nil
	}
	var problems []string
	for _, part := range splitSQL(sqlText) {
		if msg := checkOne(part, file); msg != "" {
			problems = append(problems, msg)
		}
	}
	return problems
}

func checkOne(sqlText, file string) string {
	tables := householdTablesIn(sqlText)
	if len(tables) == 0 {
		return ""
	}
	if allowed(file, sqlText) {
		return ""
	}
	if insertHead.MatchString(sqlText) {
		m := insertHead.FindStringSubmatch(sqlText)
		table := strings.ToLower(m[1])
		cols := strings.ToLower(m[2])
		if _, ok := householdSet()[table]; ok && strings.Contains(cols, "household_id") {
			// The insert names its household. A following SELECT in the same
			// statement still has to be checked when it is not this insert.
			rest := insertHead.ReplaceAllString(sqlText, "")
			if len(householdTablesIn(rest)) == 0 || scopedPred.MatchString(sqlText) {
				return ""
			}
		}
	}
	if scopedPred.MatchString(sqlText) {
		return ""
	}
	return fmt.Sprintf("unscoped household SQL on %s: %s", strings.Join(tables, ", "), oneLine(sqlText))
}

func allowed(file, sqlText string) bool {
	slash := filepath.ToSlash(file)
	for _, entry := range sqlAllow {
		if entry.reason == "" {
			continue
		}
		if strings.HasSuffix(slash, entry.file) && strings.Contains(sqlText, entry.snippet) {
			return true
		}
	}
	return false
}

func householdTablesIn(sqlText string) []string {
	set := householdSet()
	seen := map[string]bool{}
	var out []string
	for _, match := range tableRef.FindAllStringSubmatch(sqlText, -1) {
		name := strings.ToLower(match[1])
		if !set[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func householdSet() map[string]bool {
	set := make(map[string]bool, len(HouseholdTables))
	for _, name := range HouseholdTables {
		set[name] = true
	}
	return set
}

func splitSQL(sqlText string) []string {
	var parts []string
	for _, part := range strings.Split(sqlText, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func oneLine(sqlText string) string {
	line := strings.Join(strings.Fields(sqlText), " ")
	if len(line) > 180 {
		return line[:180] + "..."
	}
	return line
}

// ScanRepo walks production Go sources and returns unscoped household SQL.
// Migrations and tests are not production queries. file is relative to root
// when the walk started at the module root.
func ScanRepo(root string) ([]string, error) {
	var problems []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "frontend", "mobile", "web", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		problems = append(problems, scanGoFile(path, filepath.ToSlash(rel))...)
		return nil
	})
	return problems, err
}

func scanGoFile(path, rel string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return []string{rel + ": " + err.Error()}
	}
	var problems []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, problem := range checkStatement(value, rel) {
			pos := fset.Position(lit.Pos())
			problems = append(problems, fmt.Sprintf("%s:%d: %s", rel, pos.Line, problem))
		}
		return true
	})
	return problems
}
