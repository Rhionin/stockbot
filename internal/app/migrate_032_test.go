package app

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
)

const cedar032Household = "0190bbbb-cccc-7000-8000-0000000003c2"

func openPre032(t *testing.T) (*sql.DB, string) {
	t.Helper()
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := RunMigrationsThrough(conn, groupGoalsMigration); err != nil {
		t.Fatal(err)
	}
	var household string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	return conn, household
}

// seed032 builds one synthetic household per migration rule, plus a second
// household so the goal is decided per household.
func seed032(t *testing.T, conn *sql.DB, household string) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedar032Household)
	exec(`UPDATE households SET trip_seq = 2 WHERE id = ?`, household)

	type member struct {
		product, unit, kind string
		value               float64
		hasTarget           bool
	}
	type groupSeed struct {
		id, household string
		window        any
		qty           any
		dim           any
		members       []member
	}
	amount := func(product, unit string, v float64) member {
		return member{product: product, unit: unit, kind: "amount", value: v, hasTarget: true}
	}
	months := func(product string, v float64) member {
		return member{product: product, unit: "ct", kind: "months", value: v, hasTarget: true}
	}
	none := func(product string) member { return member{product: product, unit: "bag"} }
	groups := []groupSeed{
		{id: "g-mixed", household: household, members: []member{amount("p-can", "can", 2), amount("p-oz", "oz", 3), months("p-mo", 4), none("p-bare")}},
		{id: "g-months", household: household, members: []member{months("p-m5", 5), months("p-m2", 2)}},
		{id: "g-tie", household: household, members: []member{amount("p-t1", "fl oz", 2), amount("p-t2", "lb", 2)}},
		{id: "g-window", household: household, window: 6, members: []member{amount("p-w1", "can", 4)}},
		{id: "g-qty", household: household, qty: 680.0, dim: "mass", members: []member{months("p-q1", 3)}},
		{id: "g-stop", household: household, window: 0, members: []member{amount("p-d1", "can", 5)}},
		{id: "g-zero", household: household, members: []member{amount("p-z2", "can", 4), {product: "p-z1", unit: "can", kind: "months", value: 0, hasTarget: true}}},
		{id: "g-onlyzero", household: household, members: []member{{product: "p-n1", unit: "can", kind: "amount", value: 0, hasTarget: true}}},
		{id: "g-cedar", household: cedar032Household, members: []member{amount("p-cedar", "bag", 7)}},
	}
	for _, g := range groups {
		exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, window_months, quantity_base_value, quantity_dimension)
			VALUES (?, ?, 'user-1', ?, ?, ?, ?, ?)`, g.id, g.household, g.id, g.id, g.window, g.qty, g.dim)
		for _, m := range g.members {
			exec(`INSERT INTO products (id, name, unit_of_measure) VALUES (?, ?, ?)`, m.product, m.product, m.unit)
			exec(`INSERT INTO items (id, household_id, user_id, product_id) VALUES (?, ?, 'user-1', ?)`, "i-"+m.product, g.household, m.product)
			exec(`INSERT INTO product_group_members (household_id, product_id, group_id) VALUES (?, ?, ?)`, g.household, m.product, g.id)
			if m.hasTarget {
				exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, ?, ?, ?)`, g.household, "i-"+m.product, m.kind, m.value)
			}
		}
	}
	exec(`INSERT INTO products (id, name, unit_of_measure) VALUES ('p-solo', 'p-solo', 'can')`)
	exec(`INSERT INTO items (id, household_id, user_id, product_id) VALUES ('i-p-solo', ?, 'user-1', 'p-solo')`, household)
	exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, 'i-p-solo', 'amount', 6)`, household)

	exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, touched, group_id)
		VALUES ('l-swap', ?, 'user-1', 'i-p-can', 1, 'auto', 1, 'g-mixed')`, household)
	exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, touched)
		VALUES ('l-adjusted', ?, 'user-1', 'i-p-solo', 1, 'auto', 1)`, household)
	exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, touched)
		VALUES ('l-plain', ?, 'user-1', 'i-p-solo', 1, 'auto', 0)`, household)
	exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, touched)
		VALUES ('l-manual', ?, 'user-1', 'i-p-solo', 1, 'manual', 1)`, household)
}

func dumpRows(t *testing.T, conn *sql.DB, query string) string {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, v := range values {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func state032(t *testing.T, conn *sql.DB) string {
	t.Helper()
	return strings.Join([]string{
		dumpRows(t, conn, `SELECT household_id, item_id, kind, value FROM item_targets ORDER BY household_id, item_id`),
		dumpRows(t, conn, `SELECT id, window_months, quantity_base_value, quantity_dimension FROM product_groups ORDER BY id`),
		dumpRows(t, conn, `SELECT id, touched FROM shopping_list_items ORDER BY id`),
	}, "\n--\n")
}

func goalOf(t *testing.T, conn *sql.DB, id string) string {
	t.Helper()
	return dumpRows(t, conn, `SELECT window_months, quantity_base_value, quantity_dimension, goal_items FROM product_groups WHERE id = '`+id+`'`)
}

func targetOf(t *testing.T, conn *sql.DB, item string) string {
	t.Helper()
	return dumpRows(t, conn, `SELECT kind, value FROM item_targets WHERE item_id = '`+item+`'`)
}

func assert032Golden(t *testing.T, conn *sql.DB) {
	t.Helper()
	goals := map[string]string{
		"g-mixed":    "NULL|NULL|NULL|3",
		"g-months":   "5|NULL|NULL|NULL",
		"g-tie":      "NULL|NULL|NULL|2",
		"g-window":   "6|NULL|NULL|NULL",
		"g-qty":      "NULL|680|mass|NULL",
		"g-stop":     "0|NULL|NULL|NULL",
		"g-zero":     "NULL|NULL|NULL|4",
		"g-onlyzero": "NULL|NULL|NULL|NULL",
		"g-cedar":    "NULL|NULL|NULL|7",
	}
	for id, want := range goals {
		if got := goalOf(t, conn, id); got != want {
			t.Errorf("goal %s: got %q, want %q", id, got, want)
		}
	}
	targets := map[string]string{
		"i-p-can": "", "i-p-oz": "", "i-p-mo": "", "i-p-m5": "", "i-p-m2": "",
		"i-p-t1": "", "i-p-t2": "", "i-p-w1": "", "i-p-q1": "", "i-p-d1": "",
		"i-p-z2": "", "i-p-cedar": "",
		"i-p-z1":   "months|0",
		"i-p-n1":   "amount|0",
		"i-p-solo": "amount|6",
	}
	for item, want := range targets {
		if got := targetOf(t, conn, item); got != want {
			t.Errorf("target %s: got %q, want %q", item, got, want)
		}
	}
	var backups int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM migration_backup_item_targets WHERE migration_id = '032'`).Scan(&backups); err != nil {
		t.Fatal(err)
	}
	if backups != 12 {
		t.Errorf("backed-up targets: got %d, want 12", backups)
	}
	if got := dumpRows(t, conn, `SELECT household_id, kind, value FROM migration_backup_item_targets WHERE item_id = 'i-p-cedar'`); got != cedar032Household+"|amount|7" {
		t.Errorf("cedar backup: %q", got)
	}
	if got := dumpRows(t, conn, `SELECT group_id, new_kind, new_value FROM migration_backup_group_goals ORDER BY group_id`); got != "g-cedar|items|7\ng-mixed|items|3\ng-months|months|5\ng-tie|items|2\ng-zero|items|4" {
		t.Errorf("goal backups:\n%s", got)
	}
	lines := map[string]string{
		"l-swap":     "1|3",
		"l-adjusted": "0|NULL",
		"l-plain":    "0|NULL",
		"l-manual":   "1|NULL",
	}
	for id, want := range lines {
		if got := dumpRows(t, conn, `SELECT touched, touched_expires_trip FROM shopping_list_items WHERE id = '`+id+`'`); got != want {
			t.Errorf("line %s: got %q, want %q", id, got, want)
		}
	}
	if got := dumpRows(t, conn, `SELECT entry_id, old_touched FROM migration_backup_list_edits ORDER BY entry_id`); got != "l-adjusted|1\nl-swap|1" {
		t.Errorf("list edit backups:\n%s", got)
	}
}

func TestMigration032Golden(t *testing.T) {
	conn, household := openPre032(t)
	seed032(t, conn, household)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	assert032Golden(t, conn)

	before := state032(t, conn)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if after := state032(t, conn); after != before {
		t.Fatalf("second start changed data:\n%s\nvs\n%s", before, after)
	}
}

func TestMigration032DataRerunIsNoOp(t *testing.T) {
	conn, household := openPre032(t)
	seed032(t, conn, household)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	before := state032(t, conn) + dumpRows(t, conn, `SELECT id, touched_expires_trip FROM shopping_list_items ORDER BY id`)
	raw, err := migrationsFS.ReadFile("migrations/" + groupGoalsMigration)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "INSERT INTO migration_backup_item_targets")
	if start < 0 {
		t.Fatal("032 data section not found")
	}
	if _, err := conn.Exec(text[start:]); err != nil {
		t.Fatalf("re-run data: %v", err)
	}
	after := state032(t, conn) + dumpRows(t, conn, `SELECT id, touched_expires_trip FROM shopping_list_items ORDER BY id`)
	if after != before {
		t.Fatalf("re-running the data section changed data:\n%s\nvs\n%s", before, after)
	}
	assert032Golden(t, conn)
}

func TestMigration032GoalChecks(t *testing.T) {
	conn, household := openPre032(t)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) error {
		_, err := conn.Exec(query)
		return err
	}
	if err := exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, goal_items) VALUES ('g-ok', '` + household + `', 'user-1', 'ok', 'ok', 2)`); err != nil {
		t.Fatalf("items goal: %v", err)
	}
	for name, query := range map[string]string{
		"items 0":             `UPDATE product_groups SET goal_items = 0 WHERE id = 'g-ok'`,
		"items 1000":          `UPDATE product_groups SET goal_items = 1000 WHERE id = 'g-ok'`,
		"items plus window":   `UPDATE product_groups SET window_months = 3 WHERE id = 'g-ok'`,
		"items plus quantity": `UPDATE product_groups SET quantity_base_value = 10, quantity_dimension = 'mass' WHERE id = 'g-ok'`,
		"window then items":   `INSERT INTO product_groups (id, household_id, user_id, name, name_key, window_months, goal_items) VALUES ('g-bad', '` + household + `', 'user-1', 'bad', 'bad', 3, 2)`,
	} {
		if err := exec(query); err == nil {
			t.Errorf("%s: CHECK allowed it", name)
		}
	}
}

func TestMigration032RestoreRoundTrip(t *testing.T) {
	conn, household := openPre032(t)
	seed032(t, conn, household)
	before := state032(t, conn)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if state032(t, conn) == before {
		t.Fatal("032 changed nothing")
	}
	restore, err := os.ReadFile("testdata/032_restore.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(restore)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if after := state032(t, conn); after != before {
		t.Fatalf("restore did not bring back the pre-032 state:\n%s\nvs\n%s", before, after)
	}
	if got := dumpRows(t, conn, `SELECT COUNT(*) FROM product_groups WHERE goal_items IS NOT NULL`); got != "0" {
		t.Fatalf("items goals left after restore: %s", got)
	}
}

func TestMigration032RollsBackOnFailure(t *testing.T) {
	conn, household := openPre032(t)
	seed032(t, conn, household)
	baseline, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}
	before := state032(t, conn)
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != groupGoalsMigration {
			return sqlText
		}
		return sqlText + ";\nCREATE TABLE migration032_fail_probe (x TEXT, x TEXT);\n"
	}
	if err := RunMigrations(conn); err == nil {
		t.Fatal("expected migration 032 to fail")
	}
	prepareMigrationSQL = orig
	got, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}
	if got != baseline {
		t.Fatal("schema changed after a rolled-back 032")
	}
	if after := state032(t, conn); after != before {
		t.Fatalf("data changed after a rolled-back 032:\n%s", after)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("rerun after rollback: %v", err)
	}
	assert032Golden(t, conn)
}
