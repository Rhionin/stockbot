package server

import (
	"context"
	"testing"
	"time"
)

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
