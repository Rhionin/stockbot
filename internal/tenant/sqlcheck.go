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
	tableAliasRef = regexp.MustCompile(`(?i)\b(?:from|join|update|into|table)\s+([a-z_][a-z0-9_]*)(?:\s+(?:as\s+)?([a-z_][a-z0-9_]*))?`)
	bareHousehold = regexp.MustCompile(`(?i)(?:^|[^\w.])household_id\b`)
	sqlStart      = regexp.MustCompile(`(?i)\b(select|insert|update|delete|with)\b`)
)

// notAlias is the word after a table name that is SQL, not a table alias.
var notAlias = map[string]bool{
	"and": true, "as": true, "asc": true, "between": true, "by": true, "case": true,
	"cross": true, "desc": true, "distinct": true, "else": true, "end": true,
	"exists": true, "from": true, "full": true, "group": true, "having": true,
	"in": true, "inner": true, "into": true, "is": true, "join": true, "left": true,
	"like": true, "limit": true, "natural": true, "not": true, "null": true,
	"on": true, "or": true, "order": true, "outer": true, "right": true,
	"select": true, "set": true, "then": true, "union": true, "using": true,
	"values": true, "when": true, "where": true,
}

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
	refs := householdRefs(sqlText)
	if len(refs) == 0 {
		return ""
	}
	if allowed(file, sqlText) {
		return ""
	}
	if missing := unconstrainedRefs(sqlText, refs); len(missing) > 0 {
		return fmt.Sprintf("unscoped household SQL on %s: %s", strings.Join(missing, ", "), oneLine(sqlText))
	}
	return ""
}

type hhRef struct {
	table string
	alias string
}

// householdRefs lists each household table and the alias the statement uses.
// The same table under two aliases is two references. Each one has to name
// household_id, so a join cannot ride on another table's predicate.
func householdRefs(sqlText string) []hhRef {
	set := householdSet()
	seen := map[string]bool{}
	var out []hhRef
	for _, match := range tableAliasRef.FindAllStringSubmatch(sqlText, -1) {
		table := strings.ToLower(match[1])
		if !set[table] {
			continue
		}
		alias := table
		if len(match) > 2 && match[2] != "" && !notAlias[strings.ToLower(match[2])] {
			alias = strings.ToLower(match[2])
		}
		key := table + "\x00" + alias
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hhRef{table: table, alias: alias})
	}
	return out
}

func unconstrainedRefs(sqlText string, refs []hhRef) []string {
	bare := len(bareHousehold.FindAllString(sqlText, -1))
	var missing []string
	var pending []string
	for _, ref := range refs {
		if aliasScoped(sqlText, ref.alias) {
			continue
		}
		if ref.alias != ref.table {
			missing = append(missing, ref.alias)
			continue
		}
		pending = append(pending, ref.table)
	}
	if bare < len(pending) {
		missing = append(missing, pending...)
	}
	return missing
}

func aliasScoped(sqlText, alias string) bool {
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(alias) + `\.household_id\b`).MatchString(sqlText)
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
