package tenant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnscopedSQLLintPassesOnRepo(t *testing.T) {
	root := moduleRoot(t)
	problems, err := ScanRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("unscoped household SQL:\n%s", strings.Join(problems, "\n"))
	}
}

func TestUnscopedSQLLintRejectsEveryHouseholdTable(t *testing.T) {
	for _, table := range HouseholdTables {
		table := table
		t.Run(table, func(t *testing.T) {
			selectSQL := "SELECT id FROM " + table + " WHERE id = ?"
			if got := CheckStatement(selectSQL); len(got) == 0 {
				t.Fatalf("select without household_id was allowed: %s", selectSQL)
			}
			updateSQL := "UPDATE " + table + " SET id = id WHERE id = ?"
			if got := CheckStatement(updateSQL); len(got) == 0 {
				t.Fatalf("update without household_id was allowed: %s", updateSQL)
			}
			deleteSQL := "DELETE FROM " + table + " WHERE id = ?"
			if got := CheckStatement(deleteSQL); len(got) == 0 {
				t.Fatalf("delete without household_id was allowed: %s", deleteSQL)
			}
			insertSQL := "INSERT INTO " + table + " (id) VALUES (?)"
			if got := CheckStatement(insertSQL); len(got) == 0 {
				t.Fatalf("insert without household_id was allowed: %s", insertSQL)
			}

			scopedSelect := "SELECT id FROM " + table + " WHERE household_id = ? AND id = ?"
			if got := CheckStatement(scopedSelect); len(got) != 0 {
				t.Fatalf("scoped select rejected: %v", got)
			}
			alias := "SELECT id FROM " + table + " AS t WHERE t.household_id = ?"
			if got := CheckStatement(alias); len(got) != 0 {
				t.Fatalf("aliased predicate rejected: %v", got)
			}
			scopedInsert := "INSERT INTO " + table + " (household_id, id) VALUES (?, ?)"
			if got := CheckStatement(scopedInsert); len(got) != 0 {
				t.Fatalf("scoped insert rejected: %v", got)
			}
		})
	}
}

func TestHouseholdTablesInListsEachHouseholdTableOnce(t *testing.T) {
	got := householdTablesIn(`
		SELECT id FROM items i
		JOIN items ON items.id = i.id
		JOIN products p ON p.id = i.product_id
		UPDATE categories SET name = name`)
	want := map[string]bool{"items": true, "categories": true}
	if len(got) != len(want) {
		t.Fatalf("tables = %v", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("tables = %v", got)
		}
	}
	if householdTablesIn("SELECT 1") != nil {
		t.Fatal("sql with no table named a household table")
	}
}

func TestUnscopedSQLLintAllowsGlobalCatalog(t *testing.T) {
	for table := range GlobalTables {
		sqlText := "SELECT id, name FROM " + table + " WHERE id = ?"
		if got := CheckStatement(sqlText); len(got) != 0 {
			t.Fatalf("%s: %v", table, got)
		}
	}
	joined := `
		SELECT p.name
		FROM products p
		JOIN items i ON i.product_id = p.id
		WHERE i.id = ?`
	if got := CheckStatement(joined); len(got) == 0 {
		t.Fatal("a join to items was treated as global")
	}
}

func TestUnscopedSQLLintRequiresEveryJoinedHouseholdTable(t *testing.T) {
	open := `
		SELECT ic.item_id, c.name
		FROM item_categories ic
		JOIN categories c ON c.id = ic.category_id
		WHERE ic.household_id = ?`
	got := CheckStatement(open)
	if len(got) == 0 {
		t.Fatal("a joined household table without household_id was allowed")
	}
	if !strings.Contains(got[0], "c") {
		t.Fatalf("report %q did not name the unconstrained alias", got[0])
	}
	scoped := `
		SELECT ic.item_id, c.name
		FROM item_categories ic
		JOIN categories c ON c.id = ic.category_id AND c.household_id = ic.household_id
		WHERE ic.household_id = ?`
	if got := CheckStatement(scoped); len(got) != 0 {
		t.Fatalf("scoped join rejected: %v", got)
	}
}

func TestUnscopedSQLLintAllowsPreMigrationGuard(t *testing.T) {
	sqlText := "SELECT COUNT(*) FROM items WHERE user_id NOT IN ('legacy', '')"
	if got := checkStatement(sqlText, "internal/app/household.go"); len(got) != 0 {
		t.Fatalf("guard rejected: %v", got)
	}
	if got := checkStatement(sqlText, "internal/inventory/inventory.go"); len(got) == 0 {
		t.Fatal("the same SQL was allowed outside the migration guard")
	}
}

func TestDirectScannerQueryIsAllowListed(t *testing.T) {
	sqlText := `
		SELECT d.id, d.household_id
		FROM devices d
		JOIN households h ON h.id = d.household_id
		WHERE d.kind = 'direct'
		  AND d.label = ?
		  AND d.revoked_at IS NULL
		  AND h.is_primary = 1`
	if got := checkStatement(sqlText, "internal/auth/scope.go"); len(got) != 0 {
		t.Fatalf("scanner lookup rejected: %v", got)
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
