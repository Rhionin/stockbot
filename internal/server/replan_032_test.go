package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
)

func openDBThrough031(t *testing.T) (*sql.DB, string) {
	t.Helper()
	conn, err := app.OpenSQLite(pantryTestDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrationsThrough(conn, "032_group_goals.sql"); err != nil {
		t.Fatal(err)
	}
	var hid string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&hid); err != nil {
		t.Fatal(err)
	}
	return conn, hid
}

func listRowDigest(t *testing.T, db *sql.DB, hid string) string {
	t.Helper()
	rows, err := db.Query(`SELECT id, item_id, COALESCE(group_id,''), quantity, source FROM shopping_list_items WHERE household_id = ? ORDER BY id`, hid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, item, grp, src string
		var qty int
		if err := rows.Scan(&id, &item, &grp, &qty, &src); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%d|%s", id, item, grp, qty, src))
	}
	return strings.Join(lines, "\n")
}

// TestMigration032ListReplan (B8b): 032 promotes a member target into a group
// items goal and the one-time replan removes a stale solo auto line.
func TestMigration032ListReplan(t *testing.T) {
	db, hid := openDBThrough031(t)
	catalog := product.NewCatalog(db)
	createItemViaStockIn(t, db, catalog, "user-1", "prod-f3a", "F3A", "item-f3a")
	createItemViaStockIn(t, db, catalog, "user-1", "prod-f3b", "F3B", "item-f3b")
	mustExecTest(t, db, `INSERT INTO product_groups (id, household_id, user_id, name, name_key) VALUES ('grp-f3', ?, ?, 'F3', 'f3')`, hid, hid)
	mustExecTest(t, db, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, 'grp-f3', 'prod-f3a'), (?, 'grp-f3', 'prod-f3b')`, hid, hid)
	mustExecTest(t, db, `INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, 'item-f3a', 'amount', 2)`, hid)
	beginUsing(t, db, time.Now().Add(-time.Hour))
	mustExecTest(t, db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES ('line-solo-f3', ?, ?, 'item-f3a', 1, 'auto', datetime('now'))`, hid, hid)

	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	var goal sql.NullInt64
	if err := db.QueryRow(`SELECT goal_items FROM product_groups WHERE id = 'grp-f3'`).Scan(&goal); err != nil || !goal.Valid || goal.Int64 != 2 {
		t.Fatalf("group items goal: %v err=%v", goal, err)
	}
	var targets int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_targets WHERE household_id = ? AND item_id = 'item-f3a' AND value > 0`, hid).Scan(&targets); err != nil || targets != 0 {
		t.Fatalf("member target removed: %d %v", targets, err)
	}

	now := time.Now()
	if err := ReplanAfterGroupGoals(context.Background(), db, now); err != nil {
		t.Fatal(err)
	}
	var solo int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ? AND id = 'line-solo-f3'`, hid).Scan(&solo); err != nil || solo != 0 {
		t.Fatalf("solo line remains: %d %v", solo, err)
	}
	var key string
	if err := db.QueryRow(`SELECT value FROM household_settings WHERE household_id = ? AND key = 'list_replan_032'`, hid).Scan(&key); err != nil {
		t.Fatal(err)
	}
	digest := listRowDigest(t, db, hid)

	if err := ReplanAfterGroupGoals(context.Background(), db, now); err != nil {
		t.Fatal(err)
	}
	var keyAfter string
	if err := db.QueryRow(`SELECT value FROM household_settings WHERE household_id = ? AND key = 'list_replan_032'`, hid).Scan(&keyAfter); err != nil || keyAfter != key {
		t.Fatalf("replan key changed: %q -> %q", key, keyAfter)
	}
	if got := listRowDigest(t, db, hid); got != digest {
		t.Fatalf("list changed on second replan:\n%s\nvs\n%s", digest, got)
	}
}

func TestReplanAfterGroupGoalsDropsSoloLinesOnce(t *testing.T) {
	_, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-r-a", "RA", "item-r-a")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-r-b", "RB", "item-r-b")
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, goal_items) VALUES ('grp-r', ?, ?, 'R', 'r', 2)`, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, 'grp-r', 'prod-r-a'), (?, 'grp-r', 'prod-r-b')`, hid, hid)
	beginUsing(t, env.DB, time.Now().Add(-time.Hour))
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at) VALUES ('line-solo', ?, ?, 'item-r-a', 4, 'auto', datetime('now'))`, hid, hid)

	if err := ReplanAfterGroupGoals(context.Background(), env.DB, time.Now()); err != nil {
		t.Fatal(err)
	}
	var solo int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ? AND COALESCE(group_id, '') = ''`, hid).Scan(&solo); err != nil {
		t.Fatal(err)
	}
	if solo != 0 {
		t.Fatalf("a stale solo line for a grouped product is still on the list")
	}
	var marked int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM household_settings WHERE household_id = ? AND key = 'list_replan_032'`, hid).Scan(&marked); err != nil || marked != 1 {
		t.Fatalf("refresh not recorded: %d %v", marked, err)
	}

	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at) VALUES ('line-later', ?, ?, 'item-r-b', 4, 'auto', datetime('now'))`, hid, hid)
	if err := ReplanAfterGroupGoals(context.Background(), env.DB, time.Now()); err != nil {
		t.Fatal(err)
	}
	var later int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = 'line-later'`).Scan(&later); err != nil || later != 1 {
		t.Fatalf("the refresh ran a second time: %d %v", later, err)
	}
}
