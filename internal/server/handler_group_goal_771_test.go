package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func seedGoalGroup(env testEnv, sizes map[string]string) {
	env.T.Helper()
	insertGroup(env, "g-goal", "Drinks")
	for _, id := range []string{"p-a", "p-b"} {
		mustGroupProductWithItem(env, id, "Product "+id)
		insertMember(env, "g-goal", id)
		switch sizes[id] {
		case "volume":
			mustExec(env, `UPDATE products SET net_base_value = 355, net_dimension = 'volume' WHERE id = ?`, id)
		case "mass":
			mustExec(env, `UPDATE products SET net_base_value = 340, net_dimension = 'mass' WHERE id = ?`, id)
		}
	}
}

func allVolume(env testEnv)  { seedGoalGroup(env, map[string]string{"p-a": "volume", "p-b": "volume"}) }
func oneUnsized(env testEnv) { seedGoalGroup(env, map[string]string{"p-a": "volume"}) }
func mixedSizes(env testEnv) { seedGoalGroup(env, map[string]string{"p-a": "volume", "p-b": "mass"}) }

func putGoal(body string, status int, assertions ...assertion) httpExchange {
	return httpExchange{method: http.MethodPut, path: "/api/groups/g-goal/target", body: body, expectedStatus: status, assertions: assertions}
}

func getGoalGroup(assertions ...assertion) httpExchange {
	return httpExchange{method: http.MethodGet, path: "/api/groups/g-goal", expectedStatus: http.StatusOK, assertions: assertions}
}

func noGoalSaved() httpExchange {
	return httpExchange{method: http.MethodGet, path: "/api/groups/g-goal", expectedStatus: http.StatusOK, bodyContains: []string{`"target":null`}}
}

func TestGroupGoalKinds(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name:  "items goal is saved and read back",
			setup: oneUnsized,
			httpExchange: putGoal(`{"kind":"items","value":2}`, http.StatusOK,
				assertion{path: "$.target.kind", value: "items"},
				assertion{path: "$.target.value", value: float64(2)},
				assertion{path: "$.target.unit", value: "items"},
				assertion{path: "$.target.status", value: "ok"},
			),
			afterRequest: exchanges(getGoalGroup(
				assertion{path: "$.target.kind", value: "items"},
				assertion{path: "$.windowMonths", absent: true},
				assertion{path: "$.quantity", absent: true},
			)),
		},
		{
			name:  "months goal",
			setup: oneUnsized,
			httpExchange: putGoal(`{"kind":"months","value":3}`, http.StatusOK,
				assertion{path: "$.target.kind", value: "months"},
				assertion{path: "$.target.unit", value: "months"},
				assertion{path: "$.windowMonths", value: float64(3)},
			),
		},
		{
			name:  "a first amount on an all-volume group is saved as fluid ounces with no client unit",
			setup: allVolume,
			httpExchange: putGoal(`{"kind":"amount","value":24}`, http.StatusOK,
				assertion{path: "$.target.kind", value: "amount"},
				assertion{path: "$.target.value", value: float64(24)},
				assertion{path: "$.target.unit", value: "fl oz"},
				assertion{path: "$.target.status", value: "ok"},
				assertion{path: "$.dimension", value: "volume"},
				assertion{path: "$.amountAvailable", value: true},
				assertion{path: "$.amountUnit", value: "fl oz"},
			),
		},
		{
			name:  "don't restock is its own kind",
			setup: oneUnsized,
			httpExchange: putGoal(`{"dontRestock":true}`, http.StatusOK,
				assertion{path: "$.target.kind", value: "dont_restock"},
				assertion{path: "$.target.value", value: float64(0)},
			),
		},
		{
			name:         "legacy window 0 reads as don't restock",
			setup:        oneUnsized,
			httpExchange: putGoal(`{"windowMonths":0}`, http.StatusOK, assertion{path: "$.target.kind", value: "dont_restock"}),
		},
		{
			name:         "clear goes back to the household default",
			setup:        oneUnsized,
			httpExchange: putGoal(`{"kind":"items","value":2}`, http.StatusOK),
			afterRequest: exchanges(
				putGoal(`{"clear":true}`, http.StatusOK),
				noGoalSaved(),
			),
		},
	})
}

func TestGroupGoalAmountUnavailable(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name:  "an amount on a group with an unsized member is refused and names it",
			setup: oneUnsized,
			httpExchange: putGoal(`{"kind":"amount","value":24}`, http.StatusUnprocessableEntity,
				assertion{path: "$.code", value: "amount_unavailable"},
				assertion{path: "$.blockers[0].productId", value: "p-b"},
				assertion{path: "$.blockers[0].reason", value: "size_missing"},
				assertion{path: "$.blockers[0].name", value: "Product p-b"},
			),
			afterRequest: exchanges(
				noGoalSaved(),
				getGoalGroup(
					assertion{path: "$.amountAvailable", value: false},
					assertion{path: "$.amountBlockers[0].productId", value: "p-b"},
				),
			),
		},
		{
			name:  "a legacy ounces amount on an unsized group is never stored as weight",
			setup: oneUnsized,
			httpExchange: putGoal(`{"quantity":24}`, http.StatusUnprocessableEntity,
				assertion{path: "$.code", value: "amount_unavailable"},
			),
			afterRequest: exchanges(noGoalSaved()),
		},
		{
			name:  "mixed weight and volume names the volume set on a tie",
			setup: mixedSizes,
			httpExchange: putGoal(`{"kind":"amount","value":24}`, http.StatusUnprocessableEntity,
				assertion{path: "$.code", value: "amount_unavailable"},
				assertion{path: "$.blockers[0].productId", value: "p-a"},
				assertion{path: "$.blockers[0].reason", value: "units_mismatch"},
				assertion{path: "$.blockers[0].sizeKind", value: "volume"},
				assertion{path: "$.blockers[1]", absent: true},
			),
			afterRequest: exchanges(noGoalSaved()),
		},
		{
			name:  "a client dimension that contradicts the members is refused",
			setup: allVolume,
			httpExchange: putGoal(`{"quantity":24,"dimension":"mass"}`, http.StatusUnprocessableEntity,
				assertion{path: "$.code", value: "units_mismatch"},
				assertion{path: "$.blockers[0].productId", value: "p-a"},
			),
			afterRequest: exchanges(noGoalSaved()),
		},
		{
			name:         "a client dimension that matches the members is accepted",
			setup:        allVolume,
			httpExchange: putGoal(`{"quantity":24,"dimension":"volume"}`, http.StatusOK, assertion{path: "$.target.unit", value: "fl oz"}),
		},
	})
}

func TestGroupGoalInvalid(t *testing.T) {
	cases := []struct{ name, body, field string }{
		{"items must be whole", `{"kind":"items","value":1.5}`, "value"},
		{"items above 999", `{"kind":"items","value":1000}`, "value"},
		{"items 0", `{"kind":"items","value":0}`, "value"},
		{"months 0 is not don't restock", `{"kind":"months","value":0}`, "value"},
		{"months above 12", `{"kind":"months","value":13}`, "value"},
		{"amount with two decimals", `{"kind":"amount","value":1.25}`, "value"},
		{"amount 0", `{"kind":"amount","value":0}`, "value"},
		{"unknown kind", `{"kind":"weeks","value":2}`, "kind"},
		{"kind with no value", `{"kind":"items"}`, "value"},
		{"two shapes at once", `{"kind":"items","value":2,"clear":true}`, "body"},
		{"don't restock false", `{"dontRestock":false}`, "body"},
		{"nothing sent", `{}`, "body"},
	}
	var tests []handlerTestCase
	for _, c := range cases {
		tests = append(tests, handlerTestCase{
			name:  c.name,
			setup: allVolume,
			httpExchange: putGoal(c.body, http.StatusUnprocessableEntity,
				assertion{path: "$.code", value: "invalid"},
				assertion{path: "$.field", value: c.field},
			),
			afterRequest: exchanges(noGoalSaved()),
		})
	}
	runHandlerTests(t, tests)
}

func TestGroupGoalExpectedVersion(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name:         "a stale version is refused and a current one is accepted",
			setup:        oneUnsized,
			httpExchange: putGoal(`{"kind":"items","value":2,"expectedVersion":"stale"}`, http.StatusConflict, assertion{path: "$.code", value: "conflict"}),
			afterRequest: func(env testEnv) {
				exchanges(noGoalSaved())(env)
				rec := httptest.NewRecorder()
				handlerFromEnv(env).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/groups/g-goal", nil))
				var body struct {
					Version string `json:"version"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Version == "" {
					env.T.Fatalf("no version: %v %s", err, rec.Body.String())
				}
				version := body.Version
				exchanges(
					putGoal(`{"kind":"items","value":3,"expectedVersion":"`+version+`"}`, http.StatusOK, assertion{path: "$.target.value", value: float64(3)}),
					putGoal(`{"kind":"items","value":4,"expectedVersion":"`+version+`"}`, http.StatusConflict),
					getGoalGroup(assertion{path: "$.target.value", value: float64(3)}),
				)(env)
			},
		},
	})
}

func TestGroupGoalStatusAfterMembersChange(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name:         "an unsized product added to an amount group shows size_missing",
			setup:        allVolume,
			httpExchange: putGoal(`{"kind":"amount","value":24}`, http.StatusOK, assertion{path: "$.target.status", value: "ok"}),
			afterRequest: func(env testEnv) {
				mustGroupProductWithItem(env, "p-c", "Product p-c")
				insertMember(env, "g-goal", "p-c")
				exchanges(getGoalGroup(
					assertion{path: "$.target.kind", value: "amount"},
					assertion{path: "$.target.status", value: "size_missing"},
					assertion{path: "$.target.blockers[0].productId", value: "p-c"},
					assertion{path: "$.target.blockers[0].name", value: "Product p-c"},
					assertion{path: "$.amountAvailable", value: false},
				))(env)
			},
		},
		{
			name:         "a weight product added to a volume goal shows units_mismatch",
			setup:        allVolume,
			httpExchange: putGoal(`{"kind":"amount","value":24}`, http.StatusOK),
			afterRequest: func(env testEnv) {
				mustGroupProductWithItem(env, "p-c", "Product p-c")
				mustExec(env, `UPDATE products SET net_base_value = 340, net_dimension = 'mass' WHERE id = 'p-c'`)
				insertMember(env, "g-goal", "p-c")
				exchanges(getGoalGroup(
					assertion{path: "$.target.status", value: "units_mismatch"},
					assertion{path: "$.target.blockers[0].productId", value: "p-c"},
					assertion{path: "$.target.blockers[0].sizeKind", value: "weight"},
				))(env)
			},
		},
	})
}

func TestGroupDefaultKind(t *testing.T) {
	cases := []struct {
		name, kind, value, wantKind string
		wantMonths                  float64
	}{
		{"no setting", "", "", "months", 3},
		{"household months 5", "months", "5", "months", 5},
		{"household months 0", "months", "0", "dont_restock", 0},
		{"household amount 2", "amount", "2", "amount", 3},
		{"household amount 0", "amount", "0", "dont_restock", 0},
	}
	var tests []handlerTestCase
	for _, c := range cases {
		c := c
		tests = append(tests, handlerTestCase{
			name: c.name,
			setup: func(env testEnv) {
				oneUnsized(env)
				if c.kind != "" {
					mustExec(env, `INSERT INTO household_settings (household_id, key, value) VALUES ((SELECT id FROM households WHERE is_primary = 1), 'supply_target_kind', ?), ((SELECT id FROM households WHERE is_primary = 1), 'supply_target_value', ?)`, c.kind, c.value)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/groups/g-goal",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.defaultKind", value: c.wantKind},
					{path: "$.defaultMonths", value: c.wantMonths},
				},
				bodyContains: []string{`"target":null`, `"usage":null`},
			},
		})
	}
	runHandlerTests(t, tests)
}

func TestRestockGroupedRowCarriesGroupTarget(t *testing.T) {
	restockRow := func(assertions ...assertion) httpExchange {
		return httpExchange{method: http.MethodGet, path: "/api/restock", expectedStatus: http.StatusOK, assertions: assertions}
	}
	cases := []struct {
		name, goal string
		want       []assertion
	}{
		{"items goal", `{"kind":"items","value":2}`, []assertion{
			{path: "$.items[0].level", value: "group"},
			{path: "$.items[0].groupTarget.kind", value: "items"},
			{path: "$.items[0].groupTarget.value", value: float64(2)},
			{path: "$.items[0].groupTarget.unit", value: "items"},
			{path: "$.items[0].defaultKind", absent: true},
		}},
		{"months goal", `{"kind":"months","value":4}`, []assertion{
			{path: "$.items[0].groupTarget.kind", value: "months"},
			{path: "$.items[0].groupTarget.unit", value: "months"},
		}},
		{"amount goal", `{"kind":"amount","value":24}`, []assertion{
			{path: "$.items[0].groupTarget.kind", value: "amount"},
			{path: "$.items[0].groupTarget.unit", value: "fl oz"},
			{path: "$.items[0].groupTarget.status", value: "ok"},
		}},
		{"don't restock", `{"dontRestock":true}`, []assertion{
			{path: "$.items[0].groupTarget.kind", value: "dont_restock"},
			{path: "$.items[0].groupTarget.value", value: float64(0)},
		}},
		{"no goal", `{"clear":true}`, []assertion{
			{path: "$.items[0].groupTarget", absent: true},
			{path: "$.items[0].defaultKind", value: "months"},
			{path: "$.items[0].defaultMonths", value: float64(3)},
		}},
	}
	var tests []handlerTestCase
	for _, c := range cases {
		tests = append(tests, handlerTestCase{
			name:         c.name,
			setup:        allVolume,
			httpExchange: putGoal(c.goal, http.StatusOK),
			afterRequest: exchanges(restockRow(c.want...)),
		})
	}
	tests = append(tests, handlerTestCase{
		name: "a grouped item skips its categories and follows the household",
		setup: func(env testEnv) {
			allVolume(env)
			mustExec(env, `INSERT INTO categories (id, household_id, name, name_key) VALUES ('cat-drinks', (SELECT id FROM households WHERE is_primary = 1), 'Drinks', 'drinks')`)
			mustExec(env, `INSERT INTO category_targets (household_id, category_id, kind, value) VALUES ((SELECT id FROM households WHERE is_primary = 1), 'cat-drinks', 'months', 9)`)
			mustExec(env, `INSERT INTO item_categories (household_id, item_id, category_id, source) VALUES ((SELECT id FROM households WHERE is_primary = 1), 'item-p-a', 'cat-drinks', 'user')`)
		},
		httpExchange: restockRow(
			assertion{path: "$.items[0].itemId", value: "item-p-a"},
			assertion{path: "$.items[0].level", value: "household"},
			assertion{path: "$.items[0].target.value", value: float64(3)},
		),
	})
	runHandlerTests(t, tests)
}

func TestOffListShowsABlockedGroupGoal(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "an amount goal with an unsized member is listed with its blockers",
		setup: func(env testEnv) {
			allVolume(env)
			mustExec(env, `UPDATE product_groups SET quantity_base_value = 710, quantity_dimension = 'volume' WHERE id = 'g-goal'`)
			mustGroupProductWithItem(env, "p-c", "Product p-c")
			insertMember(env, "g-goal", "p-c")
			beginUsing(env.T, env.DB, time.Now().Add(-24*time.Hour))
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/shopping-list",
			query:          map[string]string{"status": "1"},
			expectedStatus: http.StatusOK,
			assertions: []assertion{
				{path: "$.items[0]", absent: true},
				{path: "$.offList[0].id", value: "group:g-goal"},
				{path: "$.offList[0].groupId", value: "g-goal"},
				{path: "$.offList[0].name", value: "Drinks"},
				{path: "$.offList[0].offListReason", value: "group_goal_size_missing"},
				{path: "$.offList[0].blockers[0].productId", value: "p-c"},
				{path: "$.offList[0].blockers[0].name", value: "Product p-c"},
				{path: "$.offList[0].goal.kind", value: "amount"},
				{path: "$.offList[0].goal.unit", value: "fl oz"},
				{path: "$.offList[0].needsAttention", value: nil},
			},
		},
	}})
}
