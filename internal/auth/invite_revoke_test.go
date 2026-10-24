package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRevokeJoinInviteByMemberAndStartInviteByOperator(t *testing.T) {
	db := openAuthDB(t)
	dir := t.TempDir()
	svc := NewService(db, dir)
	ctx := context.Background()
	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RedeemSetup(ctx, code, "Ada Lovelace", "ada@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	ada, err := svc.Authenticate(ctx, "ada@pantry.test", "correct horse")
	if err != nil {
		t.Fatal(err)
	}

	memberJoin, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	bea, err := svc.AcceptInvite(ctx, "", memberJoin.Code, "Bea", "bea@pantry.test", "correct horse", "", "", "web")
	if err != nil || bea.Role != "member" {
		t.Fatalf("bea %#v %v", bea, err)
	}

	join, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeInvite(ctx, bea.Session.User.ID, house, join.ID, false); err != nil {
		t.Fatalf("member revoke: %v", err)
	}

	needsHouse, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	expectField(t, svc.RevokeInvite(ctx, bea.Session.User.ID, "", needsHouse.ID, false), CodeValidation, "householdId")
	expectField(t, svc.RevokeInvite(ctx, bea.Session.User.ID, "   ", needsHouse.ID, false), CodeValidation, "householdId")
	expectCode(t, svc.RevokeInvite(ctx, bea.Session.User.ID, "other-house", needsHouse.ID, false), CodeNotFound)
	if err := svc.RevokeInvite(ctx, ada.ID, house, needsHouse.ID, false); err != nil {
		t.Fatalf("owner revoke after rejected attempts: %v", err)
	}

	start, err := svc.CreateStartInvite(ctx, ada.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, svc.RevokeInvite(ctx, bea.Session.User.ID, house, start.ID, false), CodeNotFound)
	if err := svc.RevokeInvite(ctx, ada.ID, "", start.ID, true); err != nil {
		t.Fatalf("operator revoke without household: %v", err)
	}

	again, err := svc.CreateStartInvite(ctx, ada.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeInvite(ctx, ada.ID, "0190aaaa-aaaa-7000-8000-000000000099", again.ID, true); err != nil {
		t.Fatalf("operator revoke ignores household: %v", err)
	}
}

func expectField(t *testing.T, err error, code, field string) {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != code || ae.Field != field {
		t.Fatalf("got %#v want %s field %s", err, code, field)
	}
}
