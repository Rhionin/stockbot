package auth

import (
	"testing"
	"time"
)

func TestListAllHouseholdsForOperatorOrdersAndMembers(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Annex', 'auto', ?, 'abcdef0123456789abcdef0123456789ad', 0)`, other, stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		other, userID, stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 || !list[0].IsPrimary || list[0].ID != primary {
		t.Fatalf("order %#v primary %s", list, primary)
	}
	found := false
	for _, row := range list {
		if row.ID == other {
			found = true
			if len(row.Members) == 0 || row.Members[0].Role != "owner" {
				t.Fatalf("members %#v", row.Members)
			}
		}
	}
	if !found {
		t.Fatal("annex missing")
	}
}

func TestRenameHouseholdRejectsNonMemberWithHouseholdNotFound(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	expectCode(t, take2(svc.RenameHousehold(ctx, userID, "missing-household", "Nope")), CodeHouseholdNotFound)
	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Solo', '', ?, 'abcdef0123456789abcdef0123456789ae', 0)`, other, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.RenameHousehold(ctx, userID, other, "Hack")), CodeHouseholdNotFound)
	_ = primary
}
