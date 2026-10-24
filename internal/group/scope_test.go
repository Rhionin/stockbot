package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestZeroScopeRejected(t *testing.T) {
	groups, _, _ := newTestGroups(t)
	_, err := groups.List(context.Background(), tenant.Scope{})
	if !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("err = %v", err)
	}
}

func TestHouseholdCannotTouchAnotherHouseholdGroup(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "oats", "Rolled oats")

	const otherID = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', ?, ?, 0)`,
		otherID, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "c0ffee00c0ffee00c0ffee00c0ffee00"); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(otherID)
	if err != nil {
		t.Fatal(err)
	}

	view, err := groups.Create(ctx, other, "Cedar oats", []string{"oats"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := groups.Get(ctx, scope, view.ID); err == nil {
		t.Fatal("read another household's group")
	}
	listed, err := groups.List(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("list leaked %+v", listed)
	}
	memberOf, err := groups.MemberGroupID(ctx, scope, "oats")
	if err != nil || memberOf != "" {
		t.Fatalf("member group %q err %v", memberOf, err)
	}
	if _, err := groups.Rename(ctx, scope, view.ID, "Taken oats"); err == nil {
		t.Fatal("renamed another household's group")
	}
	if _, err := groups.SetTarget(ctx, scope, view.ID, &TargetInput{Clear: true}); err == nil {
		t.Fatal("updated another household's group")
	}
	if err := groups.Delete(ctx, scope, view.ID); err == nil {
		t.Fatal("deleted another household's group")
	}

	got, err := groups.Get(ctx, other, view.ID)
	if err != nil || got.Name != "Cedar oats" || len(got.Members) != 1 || got.Members[0].ProductID != "oats" {
		t.Fatalf("got %+v err %v", got, err)
	}
}
