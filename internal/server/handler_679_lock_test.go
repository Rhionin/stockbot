package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
)

// TestLock19RestockTargetOwnerAndMember — lock 19: owner and member may set restock targets.
func TestLock19RestockTargetOwnerAndMember(t *testing.T) {
	db, _, handler, owner, house, userID := stage1Owner(t, "Lock Owner", "lock-owner@pantry.test")
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", invite.Code, invite.Body.String())
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	accepted := owner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Lock Member", "email": "lock-member@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	member := &stage1Caller{t: t, h: handler, cookie: cookieFrom(t, accepted, stage1Site(t).SessionCookie().Name), house: house}
	execSQL(t, db, `INSERT INTO products (id, name, category) VALUES ('prod-lock', 'Lock Beans', 'c')`)
	execSQL(t, db, `INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-lock', ?, ?, 'prod-lock')`, userID, house)
	execSQL(t, db, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES ('grp-lock', ?, ?, 'Lock Group', 'lock-group', 'same_as_ran_out', 1)`, house, userID)
	for _, caller := range []*stage1Caller{owner, member} {
		if rec := caller.authed(http.MethodGet, "/api/shopping-list", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s GET list %d %s", callerLabel(caller, owner), rec.Code, rec.Body.String())
		}
		if rec := caller.authed(http.MethodPost, "/api/restock/items/item-lock/dont-restock", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s dont-restock %d %s", callerLabel(caller, owner), rec.Code, rec.Body.String())
		}
		if rec := caller.authed(http.MethodPut, "/api/groups/grp-lock/target", map[string]any{"windowMonths": 6}); rec.Code != http.StatusOK {
			t.Fatalf("%s group target %d %s", callerLabel(caller, owner), rec.Code, rec.Body.String())
		}
	}
}

func callerLabel(c, owner *stage1Caller) string {
	if c == owner {
		return "owner"
	}
	return "member"
}