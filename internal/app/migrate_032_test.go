package app

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const migration032Household2 = "0190bbbb-cccc-7000-8000-000000000772"

func TestMigration032Golden(t *testing.T) {
	conn, _ := openMigration032GoldenDB(t)
	defer conn.Close()
	want := captureMigration032State(t, conn)
	if err := rerunMigration032DML(conn); err != nil {
		t.Fatal(err)
	}
	assertMigration032StateEqual(t, want, captureMigration032State(t, conn))
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	assertMigration032StateEqual(t, want, captureMigration032State(t, conn))
}

func TestMigration032Checks(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	var h1 string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&h1); err != nil {
		t.Fatal(err)
	}
	mustExec032(t, conn, `INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule, rule_confirmed)
		VALUES ('chk-g', ?, ?, 'Check', 'check-g', 'favorite', 1)`, h1, h1)
	rejects := []struct {
		name string
		sql  string
	}{
		{"goal_items 0", `UPDATE product_groups SET goal_items = 0 WHERE id = 'chk-g'`},
		{"goal_items 1000", `UPDATE product_groups SET goal_items = 1000 WHERE id = 'chk-g'`},
		{"goal_items with window_months", `UPDATE product_groups SET goal_items = 2, window_months = 3 WHERE id = 'chk-g'`},
	}
	for _, tc := range rejects {
		if _, err := conn.Exec(tc.sql); err == nil {
			t.Fatalf("%s should be rejected", tc.name)
		}
	}
	if _, err := conn.Exec(`UPDATE product_groups SET quantity_base_value = 100, quantity_dimension = 'mass' WHERE id = 'chk-g'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE product_groups SET goal_items = 2 WHERE id = 'chk-g'`); err == nil {
		t.Fatal("goal_items with amount goal should be rejected")
	}
	accepts := []int{1, 999}
	for _, n := range accepts {
		if _, err := conn.Exec(`UPDATE product_groups SET quantity_base_value = NULL, quantity_dimension = NULL, window_months = NULL, goal_items = NULL WHERE id = 'chk-g'`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`UPDATE product_groups SET goal_items = ? WHERE id = 'chk-g'`, n); err != nil {
			t.Fatalf("goal_items %d should be accepted: %v", n, err)
		}
	}
}

func TestMigration032RestoreRoundTrip(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrationsThrough(conn, groupGoalsMigration); err != nil {
		t.Fatal(err)
	}
	seedMigration032GoldenFixture(t, conn)
	before := captureMigration032CoreStatePre032(t, conn)
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(Migration032RestoreSQL); err != nil {
		t.Fatal(err)
	}
	after := captureMigration032CoreStatePre032(t, conn)
	assertRowSetsEqual(t, "restore round trip", before, after)
}

func TestMigration032RestoreSkipsChangedRows(t *testing.T) {
	conn, h1 := openMigration032GoldenDB(t)
	defer conn.Close()
	mustExec032(t, conn, `UPDATE product_groups SET goal_items = 4 WHERE id = 'g1' AND household_id = ?`, h1)
	mustExec032(t, conn, `UPDATE product_groups SET window_months = 6, goal_items = NULL WHERE id = 'g2' AND household_id = ?`, h1)
	mustExec032(t, conn, `INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, 'i-g1a', 'amount', 0)
		ON CONFLICT (household_id, item_id) DO UPDATE SET kind = excluded.kind, value = excluded.value`, h1)
	if _, err := conn.Exec(Migration032RestoreSQL); err != nil {
		t.Fatal(err)
	}
	var g1Items, g2Months sql.NullInt64
	if err := conn.QueryRow(`SELECT goal_items FROM product_groups WHERE id = 'g1' AND household_id = ?`, h1).Scan(&g1Items); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT window_months FROM product_groups WHERE id = 'g2' AND household_id = ?`, h1).Scan(&g2Months); err != nil {
		t.Fatal(err)
	}
	if !g1Items.Valid || g1Items.Int64 != 4 {
		t.Fatalf("g1 goal_items = %v, want 4", g1Items)
	}
	if !g2Months.Valid || g2Months.Int64 != 6 {
		t.Fatalf("g2 window_months = %v, want 6", g2Months)
	}
	var g1Member0 int
	if err := conn.QueryRow(`SELECT value FROM item_targets WHERE household_id = ? AND item_id = 'i-g1a'`, h1).Scan(&g1Member0); err != nil {
		t.Fatal(err)
	}
	if g1Member0 != 0 {
		t.Fatalf("g1 member 0 overwritten: %d", g1Member0)
	}
	var g4Goal sql.NullInt64
	if err := conn.QueryRow(`SELECT goal_items FROM product_groups WHERE id = 'g4' AND household_id = ?`, h1).Scan(&g4Goal); err != nil {
		t.Fatal(err)
	}
	if g4Goal.Valid {
		t.Fatalf("g4 goal_items should clear: %v", g4Goal)
	}
	var restored int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM item_targets WHERE household_id = ? AND item_id IN ('i-g4a', 'i-g4b') AND value > 0`, h1).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != 2 {
		t.Fatalf("g4 amounts not restored: %d", restored)
	}
	rows, err := conn.Query(`SELECT item_id, kind, value FROM item_targets WHERE household_id = ? AND item_id IN ('i-g1b', 'i-g1c') ORDER BY item_id`, h1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, kind string
		var value float64
		if err := rows.Scan(&itemID, &kind, &value); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var g1b, g1c float64
	if err := conn.QueryRow(`SELECT value FROM item_targets WHERE household_id = ? AND item_id = 'i-g1b'`, h1).Scan(&g1b); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT value FROM item_targets WHERE household_id = ? AND item_id = 'i-g1c'`, h1).Scan(&g1c); err != nil {
		t.Fatal(err)
	}
	if g1b != 3 || g1c != 4 {
		t.Fatalf("g1 other members not restored: %v %v", g1b, g1c)
	}
}

func TestMigration032RollsBackOnFailure(t *testing.T) {
	cases := []struct {
		name   string
		fileDB bool
		marker string
	}{
		{name: "after goal_items column in memory", marker: "ALTER TABLE product_groups ADD COLUMN goal_items"},
		{name: "after last statement in memory", marker: "WHERE source = 'auto' AND touched = 1 AND touched_expires_trip IS NULL"},
		{name: "after last statement on WAL file", fileDB: true, marker: "WHERE source = 'auto' AND touched = 1 AND touched_expires_trip IS NULL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, cleanup := openPre032Database(t, tc.fileDB)
			defer cleanup()
			seedMigration032GoldenFixture(t, conn)
			baseline, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			inject032FailAfter(t, tc.marker)
			if err := RunMigrations(conn); err == nil {
				t.Fatal("expected migration 032 to fail")
			}
			cleanupMigration032TestArtifacts(t)
			got, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			if got != baseline {
				t.Fatalf("sqlite_master changed after rolled-back 032")
			}
			if err := RunMigrations(conn); err != nil {
				t.Fatalf("rerun after rollback: %v", err)
			}
			openMigration032GoldenDBOnConn(t, conn)
		})
	}
}

func openMigration032GoldenDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsThrough(conn, groupGoalsMigration); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	h1 := seedMigration032GoldenFixture(t, conn)
	if err := RunMigrations(conn); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, h1
}

func openMigration032GoldenDBOnConn(t *testing.T, conn *sql.DB) {
	t.Helper()
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, groupGoalsMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("032 not recorded after rollback rerun: %d", recorded)
	}
	cols, err := tableColumns(conn, "product_groups")
	if err != nil {
		t.Fatal(err)
	}
	if !cols["goal_items"] {
		t.Fatal("goal_items column missing after 032")
	}
}

func seedMigration032GoldenFixture(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var h1 string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&h1); err != nil {
		t.Fatal(err)
	}
	mustExec032(t, conn, `UPDATE households SET trip_seq = 4 WHERE id = ?`, h1)
	mustExec032(t, conn, `
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, trip_seq)
		VALUES (?, 'Household Two', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef012345678a', 0, 9)`,
		migration032Household2)
	mustExec032(t, conn, `INSERT INTO history_repair (household_id, id) VALUES (?, 1)`, migration032Household2)

	products := []string{"p-g1a", "p-g1b", "p-g1c", "p-g2a", "p-g2b", "p-g3a", "p-g4a", "p-g4b",
		"p-g5a", "p-g6a", "p-g7a", "p-g8a", "p-g8b", "p-g9a", "p-ung"}
	for _, id := range products {
		mustExec032(t, conn, `INSERT INTO products (id, name, unit_of_measure) VALUES (?, ?, 'each')`, id, id)
	}
	for _, spec := range []struct {
		itemID, productID, household string
	}{
		{"i-g1a", "p-g1a", h1}, {"i-g1b", "p-g1b", h1}, {"i-g1c", "p-g1c", h1},
		{"i-g2a", "p-g2a", h1}, {"i-g2b", "p-g2b", h1},
		{"i-g3a", "p-g3a", h1}, {"i-g4a", "p-g4a", h1}, {"i-g4b", "p-g4b", h1},
		{"i-g5a", "p-g5a", h1}, {"i-g6a", "p-g6a", h1}, {"i-g7a", "p-g7a", h1},
		{"i-g8a", "p-g8a", h1}, {"i-g8b", "p-g8b", h1}, {"i-ung", "p-ung", h1},
		{"i-g9a", "p-g9a", migration032Household2},
	} {
		mustExec032(t, conn, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
			spec.itemID, spec.household, spec.productID, spec.household)
	}

	groups := []struct {
		id, household, name string
		wm                  sql.NullInt64
		qb                  sql.NullFloat64
		qd                  sql.NullString
	}{
		{"g1", h1, "G1", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
		{"g2", h1, "G2", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
		{"g3", h1, "G3", sql.NullInt64{Int64: 3, Valid: true}, sql.NullFloat64{}, sql.NullString{}},
		{"g4", h1, "G4", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
		{"g5", h1, "G5", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
		{"g6", h1, "G6", sql.NullInt64{}, sql.NullFloat64{Float64: 709.76, Valid: true}, sql.NullString{String: "volume", Valid: true}},
		{"g7", h1, "G7", sql.NullInt64{Int64: 0, Valid: true}, sql.NullFloat64{}, sql.NullString{}},
		{"g8", h1, "G8", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
		{"g9", migration032Household2, "G9", sql.NullInt64{}, sql.NullFloat64{}, sql.NullString{}},
	}
	for _, g := range groups {
		mustExec032(t, conn, `INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule, rule_confirmed, window_months, quantity_base_value, quantity_dimension)
			VALUES (?, ?, ?, ?, ?, 'favorite', 1, ?, ?, ?)`,
			g.id, g.household, g.household, g.name, strings.ToLower(g.name), g.wm, g.qb, g.qd)
	}

	members := []struct{ household, productID, groupID string }{
		{h1, "p-g1a", "g1"}, {h1, "p-g1b", "g1"}, {h1, "p-g1c", "g1"},
		{h1, "p-g2a", "g2"}, {h1, "p-g2b", "g2"},
		{h1, "p-g3a", "g3"},
		{h1, "p-g4a", "g4"}, {h1, "p-g4b", "g4"},
		{h1, "p-g5a", "g5"},
		{h1, "p-g6a", "g6"},
		{h1, "p-g7a", "g7"},
		{h1, "p-g8a", "g8"}, {h1, "p-g8b", "g8"},
		{migration032Household2, "p-g9a", "g9"},
	}
	for _, m := range members {
		mustExec032(t, conn, `INSERT INTO product_group_members (household_id, product_id, group_id) VALUES (?, ?, ?)`,
			m.household, m.productID, m.groupID)
	}

	targets := []struct{ household, itemID, kind string; value float64 }{
		{h1, "i-g1a", "amount", 2}, {h1, "i-g1b", "amount", 3}, {h1, "i-g1c", "months", 4},
		{h1, "i-g2a", "months", 2}, {h1, "i-g2b", "months", 5},
		{h1, "i-g3a", "amount", 6},
		{h1, "i-g4a", "amount", 2}, {h1, "i-g4b", "amount", 2},
		{h1, "i-g5a", "months", 0},
		{h1, "i-g6a", "amount", 4},
		{h1, "i-g7a", "amount", 1},
		{h1, "i-g8a", "amount", 2}, {h1, "i-g8b", "months", 0},
		{h1, "i-ung", "amount", 5},
		{migration032Household2, "i-g9a", "amount", 7},
	}
	for _, tg := range targets {
		mustExec032(t, conn, `INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, ?, ?, ?)`,
			tg.household, tg.itemID, tg.kind, tg.value)
	}

	mustExec032(t, conn, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, touched)
		VALUES ('line-nogroup', ?, 'i-ung', 1, 'auto', ?, 1)`, h1, h1)
	mustExec032(t, conn, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, group_id, touched)
		VALUES ('line-g1', ?, 'i-g1a', 1, 'auto', ?, 'g1', 1)`, h1, h1)
	mustExec032(t, conn, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, group_id, touched)
		VALUES ('line-g2', ?, 'i-g2a', 1, 'auto', ?, 'g2', 0)`, h1, h1)
	mustExec032(t, conn, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, touched)
		VALUES ('line-manual', ?, 'i-g2b', 1, 'manual', ?, 0)`, h1, h1)
	mustExec032(t, conn, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, group_id, touched)
		VALUES ('line-h2-g9', ?, 'i-g9a', 1, 'auto', ?, 'g9', 1)`, migration032Household2, migration032Household2)
	return h1
}

type migration032State struct {
	productGroups     map[string]bool
	itemTargets       map[string]bool
	shoppingList      map[string]bool
	backupItemTargets map[string]bool
	backupGroupGoals  map[string]bool
	backupListEdits   map[string]bool
}

func captureMigration032State(t *testing.T, conn *sql.DB) migration032State {
	t.Helper()
	state := migration032State{
		productGroups:     queryRowSet(t, conn, `SELECT household_id || ':' || id || '|' || COALESCE(CAST(window_months AS TEXT), '') || '|' || COALESCE(CAST(quantity_base_value AS TEXT), '') || '|' || COALESCE(quantity_dimension, '') || '|' || COALESCE(CAST(goal_items AS TEXT), '') FROM product_groups WHERE id LIKE 'g%' OR id = 'chk-g' ORDER BY household_id, id`),
		itemTargets:       queryRowSet(t, conn, `SELECT household_id || ':' || item_id || '|' || kind || '|' || CAST(CAST(value AS INTEGER) AS TEXT) FROM item_targets ORDER BY household_id, item_id`),
		shoppingList:      queryRowSet(t, conn, `SELECT household_id || ':' || id || '|' || source || '|' || COALESCE(group_id, '') || '|' || CAST(touched AS TEXT) || '|' || COALESCE(CAST(touched_expires_trip AS TEXT), '') FROM shopping_list_items WHERE id LIKE 'line-%' ORDER BY household_id, id`),
		backupItemTargets: queryRowSet(t, conn, `SELECT household_id || ':' || item_id || '|' || product_id || '|' || group_id || '|' || kind || '|' || CAST(CAST(value AS INTEGER) AS TEXT) FROM migration_backup_item_targets WHERE migration_id = '032' ORDER BY household_id, item_id`),
		backupGroupGoals:  queryRowSet(t, conn, `SELECT household_id || ':' || group_id || '|' || new_kind || '|' || CAST(new_value AS TEXT) FROM migration_backup_group_goals WHERE migration_id = '032' ORDER BY household_id, group_id`),
		backupListEdits:   queryRowSet(t, conn, `SELECT household_id || ':' || entry_id || '|' || CAST(old_touched AS TEXT) FROM migration_backup_list_edits WHERE migration_id = '032' ORDER BY household_id, entry_id`),
	}
	assertMigration032GoldenExpectations(t, state)
	return state
}

func captureMigration032CoreState(t *testing.T, conn *sql.DB) map[string]bool {
	t.Helper()
	return captureMigration032CoreStateWithTripCol(t, conn, true)
}

func captureMigration032CoreStatePre032(t *testing.T, conn *sql.DB) map[string]bool {
	t.Helper()
	rows := queryRowSet(t, conn, `
		SELECT 'g|' || household_id || ':' || id || '|' || COALESCE(CAST(window_months AS TEXT), '') || '|' || COALESCE(CAST(quantity_base_value AS TEXT), '') || '|' || COALESCE(quantity_dimension, '')
		FROM product_groups WHERE id LIKE 'g%'
		UNION ALL
		SELECT 't|' || household_id || ':' || item_id || '|' || kind || '|' || CAST(CAST(value AS INTEGER) AS TEXT)
		FROM item_targets WHERE item_id LIKE 'i-%'
		UNION ALL
		SELECT 's|' || household_id || ':' || id || '|' || CAST(touched AS TEXT) || '|'
		FROM shopping_list_items WHERE id LIKE 'line-%'
		ORDER BY 1`)
	return rows
}

func captureMigration032CoreStateWithTripCol(t *testing.T, conn *sql.DB, includeTripCol bool) map[string]bool {
	t.Helper()
	var listSel string
	if includeTripCol {
		listSel = `SELECT 's|' || household_id || ':' || id || '|' || CAST(touched AS TEXT) || '|' || COALESCE(CAST(touched_expires_trip AS TEXT), '')
			FROM shopping_list_items WHERE id LIKE 'line-%'`
	} else {
		listSel = `SELECT 's|' || household_id || ':' || id || '|' || CAST(touched AS TEXT) || '|'
			FROM shopping_list_items WHERE id LIKE 'line-%'`
	}
	rows := queryRowSet(t, conn, `
		SELECT 'g|' || household_id || ':' || id || '|' || COALESCE(CAST(window_months AS TEXT), '') || '|' || COALESCE(CAST(goal_items AS TEXT), '')
		FROM product_groups WHERE id LIKE 'g%'
		UNION ALL
		SELECT 't|' || household_id || ':' || item_id || '|' || kind || '|' || CAST(CAST(value AS INTEGER) AS TEXT)
		FROM item_targets WHERE item_id LIKE 'i-%'
		UNION ALL
		`+listSel+`
		ORDER BY 1`)
	return rows
}

func assertMigration032GoldenExpectations(t *testing.T, state migration032State) {
	t.Helper()
	h1 := primaryHouseholdFromState(state)
	wantGroups := map[string]bool{
		h1 + ":g1||||3":                        true,
		h1 + ":g2|5|||":                        true,
		h1 + ":g3|3|||":                        true,
		h1 + ":g4||||2":                        true,
		h1 + ":g6||709.76|volume|":             true,
		h1 + ":g7|0|||":                        true,
		h1 + ":g8||||2":                        true,
		migration032Household2 + ":g9||||7":    true,
	}
	for row := range wantGroups {
		if !state.productGroups[row] {
			t.Fatalf("missing product_groups row %q in %v", row, keysOf(state.productGroups))
		}
	}
	wantBackups := 12
	if len(state.backupItemTargets) != wantBackups {
		t.Fatalf("backup item targets: got %d want %d: %v", len(state.backupItemTargets), wantBackups, keysOf(state.backupItemTargets))
	}
	for row := range state.backupItemTargets {
		if strings.HasSuffix(row, "|0") {
			t.Fatalf("backup included value 0: %s", row)
		}
	}
	wantGroupBackups := map[string]bool{
		lookupHouseholdPrefix(t, state, "g1") + "g1|items|3":  true,
		lookupHouseholdPrefix(t, state, "g2") + "g2|months|5": true,
		lookupHouseholdPrefix(t, state, "g4") + "g4|items|2":  true,
		lookupHouseholdPrefix(t, state, "g8") + "g8|items|2":  true,
		migration032Household2 + ":g9|items|7":                true,
	}
	assertRowSetsEqual(t, "backup group goals", wantGroupBackups, state.backupGroupGoals)
	if len(state.backupListEdits) != 3 {
		t.Fatalf("backup list edits: got %d", len(state.backupListEdits))
	}
	wantLines := map[string]bool{
		lookupHouseholdPrefix(t, state, "line-nogroup") + "line-nogroup|auto||0|":    true,
		lookupHouseholdPrefix(t, state, "line-g1") + "line-g1|auto|g1|1|5":           true,
		lookupHouseholdPrefix(t, state, "line-g2") + "line-g2|auto|g2|0|":             true,
		lookupHouseholdPrefix(t, state, "line-manual") + "line-manual|manual||0|":    true,
		migration032Household2 + ":line-h2-g9|auto|g9|1|10":                          true,
	}
	assertRowSetsEqual(t, "shopping list", wantLines, state.shoppingList)
	if _, ok := state.itemTargets[lookupHouseholdPrefix(t, state, "i-ung")+"i-ung|amount|5"]; !ok {
		t.Fatal("ungrouped target removed")
	}
}

func primaryHouseholdFromState(state migration032State) string {
	for row := range state.productGroups {
		if !strings.HasPrefix(row, migration032Household2+":") {
			parts := strings.SplitN(row, ":", 2)
			return parts[0]
		}
	}
	return ""
}

func lookupHousehold(t *testing.T, state migration032State, groupID string) string {
	t.Helper()
	prefix := lookupHouseholdPrefix(t, state, groupID)
	return strings.TrimSuffix(prefix, ":")
}

func lookupHouseholdPrefix(t *testing.T, state migration032State, token string) string {
	t.Helper()
	for row := range state.productGroups {
		if strings.Contains(row, ":"+token) {
			parts := strings.SplitN(row, ":", 2)
			return parts[0] + ":"
		}
	}
	for row := range state.shoppingList {
		if strings.Contains(row, ":"+token) {
			parts := strings.SplitN(row, ":", 2)
			return parts[0] + ":"
		}
	}
	if strings.HasPrefix(token, "line-") || strings.HasPrefix(token, "i-") {
		for row := range state.itemTargets {
			if strings.Contains(row, ":"+token) {
				parts := strings.SplitN(row, ":", 2)
				return parts[0] + ":"
			}
		}
		for row := range state.shoppingList {
			if strings.Contains(row, ":"+token) {
				parts := strings.SplitN(row, ":", 2)
				return parts[0] + ":"
			}
		}
	}
	if token == "g9" {
		return migration032Household2 + ":"
	}
	var h1 string
	for row := range state.productGroups {
		if !strings.HasPrefix(row, migration032Household2+":") {
			parts := strings.SplitN(row, ":", 2)
			h1 = parts[0]
			break
		}
	}
	return h1 + ":"
}

func assertMigration032StateEqual(t *testing.T, want, got migration032State) {
	t.Helper()
	assertRowSetsEqual(t, "product_groups", want.productGroups, got.productGroups)
	assertRowSetsEqual(t, "item_targets", want.itemTargets, got.itemTargets)
	assertRowSetsEqual(t, "shopping_list_items", want.shoppingList, got.shoppingList)
	assertRowSetsEqual(t, "migration_backup_item_targets", want.backupItemTargets, got.backupItemTargets)
	assertRowSetsEqual(t, "migration_backup_group_goals", want.backupGroupGoals, got.backupGroupGoals)
	assertRowSetsEqual(t, "migration_backup_list_edits", want.backupListEdits, got.backupListEdits)
}

func assertRowSetsEqual(t *testing.T, label string, want, got map[string]bool) {
	t.Helper()
	for row := range want {
		if !got[row] {
			t.Fatalf("%s missing row %q", label, row)
		}
	}
	for row := range got {
		if !want[row] {
			t.Fatalf("%s extra row %q", label, row)
		}
	}
}

func queryRowSet(t *testing.T, conn *sql.DB, query string, args ...any) map[string]bool {
	t.Helper()
	rows, err := conn.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		out[line] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func keysOf(m map[string]bool) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func rerunMigration032DML(conn *sql.DB) error {
	dml, err := migration032DMLText()
	if err != nil {
		return err
	}
	_, err = conn.Exec(dml)
	return err
}

func migration032DMLText() (string, error) {
	sqlBytes, err := migrationsFS.ReadFile(filepath.Join("migrations", groupGoalsMigration))
	if err != nil {
		return "", err
	}
	text := string(sqlBytes)
	const marker = "-- DML"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return "", fmt.Errorf("migration 032 missing DML marker")
	}
	return strings.TrimSpace(text[idx+len(marker):]), nil
}

func openPre032Database(t *testing.T, file bool) (*sql.DB, func()) {
	t.Helper()
	if file {
		path := filepath.Join(t.TempDir(), "pantry.db")
		writer, reader, err := OpenPantry(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := RunMigrationsThrough(writer, groupGoalsMigration); err != nil {
			t.Fatal(err)
		}
		return writer, func() {
			writer.Close()
			reader.Close()
		}
	}
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsThrough(conn, groupGoalsMigration); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, func() { conn.Close() }
}

func inject032FailAfter(t *testing.T, marker string) {
	t.Helper()
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != groupGoalsMigration {
			return sqlText
		}
		idx := strings.Index(sqlText, marker)
		if idx < 0 {
			t.Fatalf("032 migration missing marker %q", marker)
		}
		end := idx + len(marker)
		return sqlText[:end] + ";\nCREATE TABLE migration032_fail_probe (x TEXT, x TEXT);\n" + sqlText[end:]
	}
}

func cleanupMigration032TestArtifacts(t *testing.T) {
	t.Helper()
	prepareMigrationSQL = func(_ string, sqlText string) string { return sqlText }
}

func mustExec032(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", strings.Fields(query)[0], err)
	}
}

func TestMigration032PortableSubset(t *testing.T) {
	sqlBytes, err := migrationsFS.ReadFile("migrations/" + groupGoalsMigration)
	if err != nil {
		t.Fatal(err)
	}
	text := string(sqlBytes)
	for _, forbidden := range []string{"datetime(", "INSERT OR REPLACE", "PRAGMA"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("032 contains forbidden %q", forbidden)
		}
	}
}