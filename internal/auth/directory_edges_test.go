package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

)

func TestDirectoryEdges(t *testing.T) {
	db := openAuthDB(t)
	dir := t.TempDir()
	svc := NewService(db, dir)
	ctx := context.Background()
	if svc.Limiter() == nil {
		t.Fatal("limiter")
	}
	var unset *Service
	if unset.Now().IsZero() {
		t.Fatal("nil clock")
	}
	svc.SetClock(nil)

	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, svc.RedeemSetup(ctx, "nope", "Ada", "ada@pantry.test", "correct horse", ""), CodeNotFound)
	expectCode(t, svc.RedeemSetup(ctx, code, "Ada", "not-an-email", "correct horse", ""), CodeValidation)
	expectCode(t, svc.RedeemSetup(ctx, code, "Ada", "ada@pantry.test", "", ""), CodeValidation)
	expectCode(t, svc.RedeemSetup(ctx, code, "Ada", "ada@pantry.test", "short", ""), CodeValidation)
	expectCode(t, svc.RedeemSetup(ctx, code, "", "ada@pantry.test", "correct horse", ""), CodeValidation)
	expectCode(t, svc.RedeemSetup(ctx, code, "Ada", "ada@pantry.test", "correct horse", "another horse"), CodeValidation)
	if err := svc.RedeemSetup(ctx, code, "Ada Lovelace", "ada@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if health := svc.SetupHealth(ctx); health != "" {
		t.Fatalf("health %q", health)
	}
	if err := os.WriteFile(svc.codePath(), []byte("stale\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.codePath()); !os.IsNotExist(err) {
		t.Fatalf("boot left the display file: %v", err)
	}
	if err := svc.AdminIssue(ctx, true); err != nil {
		t.Fatal(err)
	}
	recoverCode, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.codePath()); err != nil {
		t.Fatal(err)
	}
	expectCode(t, svc.RedeemSetup(ctx, recoverCode, "Ada", "ada@pantry.test", "correct horse", ""), CodeNotFound)
	expectCode(t, svc.RedeemSetup(ctx, recoverCode, "Ada", "ada@pantry.test", "", "another horse"), CodeValidation)
	expectCode(t, svc.RedeemSetup(ctx, recoverCode, "", "bea@pantry.test", "", "another horse"), CodeNotFound)

	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	ada, err := svc.Authenticate(ctx, "ada@pantry.test", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, svc.SetPassword(ctx, ada.ID, "short"), CodeValidation)
	expectCode(t, svc.SetPassword(ctx, "missing-user", "correct horse"), CodeNotFound)
	if _, err := svc.LoginAccount(ctx, "ada@pantry.test", "wrong-password", ""); err == nil {
		t.Fatal("bad password logged in")
	}
	adaView, err := svc.LoginAccount(ctx, "ada@pantry.test", "correct horse", "expo")
	if err != nil || adaView.Token == "" {
		t.Fatalf("login %v", err)
	}
	if _, _, err := svc.LookupSession(ctx, "nope"); err == nil {
		t.Fatal("bad token looked up")
	}
	if _, _, err := svc.LookupSession(ctx, "sbs_missing"); err == nil {
		t.Fatal("unknown session looked up")
	}
	unknown, err := svc.View(ctx, Principal{Kind: "nope"})
	if err != nil || unknown.Kind != "signed_out" {
		t.Fatalf("unknown view %#v %v", unknown, err)
	}

	expectCode(t, take2(svc.CreateJoinInvite(ctx, ada.ID, house, "guest")), CodeValidation)
	expectCode(t, take2(svc.CreateJoinInvite(ctx, "missing", house, "member")), CodeNotFound)
	expectCode(t, take2(svc.CreateStartInvite(ctx, ada.ID, false)), CodeNotFound)
	if _, err := svc.ListInvites(ctx, "missing", house); err == nil {
		t.Fatal("stranger listed invites")
	}
	join, err := svc.CreateJoinInvite(ctx, ada.ID, house, "")
	if err != nil || join.Role != "member" {
		t.Fatalf("default invite %#v %v", join, err)
	}
	expectCode(t, svc.RevokeInvite(ctx, ada.ID, "other-house", join.ID, false), CodeNotFound)
	expectCode(t, svc.RevokeInvite(ctx, "missing", house, join.ID, false), CodeNotFound)
	if err := svc.RevokeInvite(ctx, ada.ID, house, join.ID, false); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.ListInvites(ctx, ada.ID, house)
	if err != nil || len(listed) != 1 || listed[0].Status != "revoked" {
		t.Fatalf("revoked list %#v %v", listed, err)
	}
	expectCode(t, svc.RevokeInvite(ctx, ada.ID, "", "missing-invite", true), CodeNotFound)

	start, err := svc.CreateStartInvite(ctx, ada.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, svc.RevokeInvite(ctx, ada.ID, "", start.ID, false), CodeNotFound)
	expectCode(t, take2(svc.AcceptInvite(ctx, ada.ID, start.Code, "", "", "", "", "", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "", "", "", "", "", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "", "not-email", "correct horse", "Cabin", "auto", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "Eve", "eve@pantry.test", "short", "Cabin", "auto", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "", "eve@pantry.test", "correct horse", "Cabin", "auto", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "Eve", "eve@pantry.test", "correct horse", "", "auto", "")), CodeValidation)
	expectCode(t, take2(svc.AcceptInvite(ctx, "", start.Code, "Eve", "eve@pantry.test", "correct horse", "Cabin", "nope", "")), CodeValidation)
	started, err := svc.AcceptInvite(ctx, "", start.Code, "Eve", "eve@pantry.test", "correct horse", "Cabin", "ask_hold", "")
	if err != nil || started.Role != "owner" || started.HouseholdID == "" {
		t.Fatalf("start accept %#v %v", started, err)
	}
	expectCode(t, take2(svc.CreateHousehold(ctx, ada.ID, "", "Loft", "auto", join.Code)), CodeNotFound)
	expectCode(t, take2(svc.CreateHousehold(ctx, ada.ID, "", " ", "auto", start.Code)), CodeValidation)

	freshStart, err := svc.CreateStartInvite(ctx, ada.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	deeHash, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	deeID := newID()
	if _, err := db.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES (?, 'Dee', 'dee@pantry.test', ?, 'local', 0, ?)`, deeID, deeHash, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	deeView, err := svc.LoginAccount(ctx, "dee@pantry.test", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	loft, err := svc.CreateHousehold(ctx, deeID, deeView.Token, "Loft", "", freshStart.Code)
	if err != nil || loft.Name != "Loft" {
		t.Fatalf("loft %#v %v", loft, err)
	}
	if _, err := svc.Settings(ctx, loft.ID); err != nil {
		t.Fatalf("loft settings %v", err)
	}
	expectCode(t, take2(svc.UpdateRestock(ctx, deeID, loft.ID, "nope")), CodeValidation)

	memberJoin, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	bea, err := svc.AcceptInvite(ctx, "", memberJoin.Code, "Bea", "bea@pantry.test", "correct horse", "", "", "web")
	if err != nil || bea.Role != "member" {
		t.Fatalf("bea %#v %v", bea, err)
	}
	expectCode(t, take2(svc.CreateJoinInvite(ctx, bea.Session.User.ID, house, "owner")), CodeForbiddenRole)
	expectCode(t, take2(svc.ChangeRole(ctx, bea.Session.User.ID, house, ada.ID, "member")), CodeForbiddenRole)
	expectCode(t, take2(svc.ChangeRole(ctx, ada.ID, house, "missing-user", "member")), CodeNotFound)
	expectCode(t, svc.RemoveMember(ctx, bea.Session.User.ID, house, ada.ID), CodeForbiddenRole)
	expectCode(t, svc.RemoveMember(ctx, ada.ID, house, "missing-user"), CodeNotFound)
	expectCode(t, take2(svc.RenameHousehold(ctx, bea.Session.User.ID, house, "Nope")), CodeForbiddenRole)
	expectCode(t, take2(svc.RenameHousehold(ctx, ada.ID, house, " ")), CodeValidation)
	expectCode(t, take2(svc.DeleteHousehold(ctx, bea.Session.User.ID, house, "Primary")), CodeForbiddenRole)
	expectCode(t, take2(svc.DeleteHousehold(ctx, bea.Session.User.ID, loft.ID, "Loft")), CodeNotFound)
	expectCode(t, take2(svc.UpdateRestock(ctx, bea.Session.User.ID, house, "auto")), CodeForbiddenRole)

	second, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	soloHash, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	soloID := newID()
	if _, err := db.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES (?, 'Fay', 'fay@pantry.test', ?, 'local', 0, ?)`, soloID, soloHash, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LoginAccount(ctx, "fay@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	joined, err := svc.AcceptInvite(ctx, soloID, second.Code, "", "", "", "", "", "")
	if err != nil || joined.Session.ActiveHouseholdID == nil || *joined.Session.ActiveHouseholdID != house {
		t.Fatalf("signed-in accept %#v %v", joined, err)
	}
	again, err := svc.CreateJoinInvite(ctx, ada.ID, house, "member")
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AcceptInvite(ctx, soloID, again.Code, "", "", "", "", "", "")), CodeInviteExpired)
	if err := svc.RevokeInvite(ctx, ada.ID, house, again.ID, false); err != nil {
		t.Fatal(err)
	}

	expectCode(t, take2(svc.CreateResetLink(ctx, ada.ID, house, "missing-user", false)), CodeNotFound)
	expectCode(t, take2(svc.CreateResetLink(ctx, bea.Session.User.ID, house, ada.ID, false)), CodeForbiddenRole)
	expectCode(t, take2(svc.CreateResetLink(ctx, "missing", house, ada.ID, false)), CodeNotFound)
	link, err := svc.CreateResetLink(ctx, ada.ID, house, bea.Session.User.ID, false)
	if err != nil || link.Code == "" {
		t.Fatalf("reset %#v %v", link, err)
	}
	expectCode(t, take2(svc.ResetPassword(ctx, "", "correct horse", "")), CodeValidation)
	svc.SetClock(func() time.Time { return time.Now().Add(48 * time.Hour) })
	for i := 0; i < maxAttempts; i++ {
		expectCode(t, take2(svc.ResetPassword(ctx, link.Code, "correct horse", "")), CodeResetExpired)
	}
	expectCode(t, take2(svc.ResetPassword(ctx, link.Code, "correct horse", "")), CodeRateLimited)
	svc.SetClock(wallClock)

	expectCode(t, svc.DeleteAccount(ctx, "missing-user", "ada@pantry.test"), CodeUnauthorized)
	expectCode(t, take2(svc.DeleteHousehold(ctx, deeID, loft.ID, "wrong")), CodeConfirm)
	deleted, err := svc.DeleteHousehold(ctx, deeID, loft.ID, "Loft")
	if err != nil || deleted.DeletedAt == nil {
		t.Fatalf("delete %#v %v", deleted, err)
	}
	expectCode(t, take2(svc.RenameHousehold(ctx, deeID, loft.ID, "Later")), CodeNotFound)
	expectCode(t, take2(svc.RestoreHousehold(ctx, ada.ID, loft.ID)), CodeNotFound)
	expectCode(t, take2(svc.RestoreHousehold(ctx, deeID, house)), CodeNotFound)
	if _, err := svc.RestoreHousehold(ctx, deeID, loft.ID); err != nil {
		t.Fatal(err)
	}

	pair, err := svc.StartPairing(ctx, ada.ID, house)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.StartPairing(ctx, "missing", house)), CodeNotFound)
	if _, err := svc.PairingStatus(ctx, ada.ID, house, "missing"); err == nil {
		t.Fatal("missing pairing")
	}
	expectCode(t, take2(svc.CompletePairing(ctx, "not a code", "Counter", "", "", "")), CodeValidation)
	expectCode(t, take2(svc.CompletePairing(ctx, "SBP-OOOO-IIII-0", "Counter", "", "", "")), CodeNotFound)
	svc.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	for i := 0; i < maxAttempts; i++ {
		expectCode(t, take2(svc.CompletePairing(ctx, pair.Code, "Counter", "", "", "")), CodeNotFound)
	}
	expectCode(t, take2(svc.CompletePairing(ctx, pair.Code, "Counter", "", "", "")), CodeRateLimited)
	if status, err := svc.PairingStatus(ctx, ada.ID, house, pair.PairingID); err != nil || status.Status != "expired" {
		t.Fatalf("expired %#v %v", status, err)
	}
	svc.SetClock(wallClock)
	live, err := svc.StartPairing(ctx, ada.ID, house)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.CompletePairing(ctx, live.Code, "Counter", "satellite", "", "")), CodeValidation)
	hub, err := svc.CompletePairing(ctx, live.Code, "", "pi_hub", "1.0.0", "")
	if err != nil || !hub.Hub || hub.Token == "" {
		t.Fatalf("hub %#v %v", hub, err)
	}
	expectCode(t, take2(svc.CompletePairing(ctx, live.Code, "Counter", "", "", "")), CodeNotFound)
	expectCode(t, take2(svc.RenameDevice(ctx, ada.ID, house, hub.DeviceID, " ")), CodeValidation)
	var directID string
	if err := db.QueryRow(`SELECT id FROM devices WHERE kind = 'direct' AND household_id = ?`, house).Scan(&directID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.RenameDevice(ctx, ada.ID, house, directID, "Nope")), CodeNotFound)

	principal, err := svc.AuthenticateDevice(ctx, hub.Token)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AuthenticateDevice(ctx, "nope")), CodeUnauthorized)
	if _, err := svc.NoteContact(ctx, principal, "1.2.3", 4, 1); err != nil {
		t.Fatal(err)
	}
	devices, err := svc.ListDevices(ctx, ada.ID, house)
	if err != nil {
		t.Fatal(err)
	}
	var sawOnline bool
	for _, d := range devices {
		if d.ID == hub.DeviceID && d.Online && d.Queued != nil && *d.Queued == 4 {
			sawOnline = true
		}
	}
	if !sawOnline {
		t.Fatalf("online devices %#v", devices)
	}
	svc.presence.markOffline(hub.DeviceID)
	fresh := NewService(db, dir)
	freshDevices, err := fresh.ListDevices(ctx, ada.ID, house)
	if err != nil {
		t.Fatal(err)
	}
	var sawStored bool
	for _, d := range freshDevices {
		if d.ID == hub.DeviceID && d.LastSeenAt != nil && d.Online {
			sawStored = true
		}
	}
	if !sawStored {
		t.Fatalf("stored presence %#v", freshDevices)
	}
	fresh.SetClock(func() time.Time { return time.Now().Add(time.Hour) })
	later, err := fresh.ListDevices(ctx, ada.ID, house)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range later {
		if d.ID == hub.DeviceID && d.Online {
			t.Fatal("stale device stayed online")
		}
	}

	next, err := svc.RotateDeviceToken(ctx, hub.Token)
	if err != nil || next.Token == "" {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateDevice(ctx, hub.Token); err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return time.Now().Add(25 * time.Hour) })
	expectCode(t, take2(svc.AuthenticateDevice(ctx, hub.Token)), CodeUnauthorized)
	svc.SetClock(wallClock)
	if _, err := svc.AuthenticateDevice(ctx, next.Token); err != nil {
		t.Fatal(err)
	}
	current, err := svc.AuthenticateDevice(ctx, next.Token)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.IngestScans(ctx, current, nil, 0)), CodeValidation)
	tooMany := make([]ScanInput, 51)
	expectCode(t, take2(svc.IngestScans(ctx, current, tooMany, 0)), CodeValidation)
	for i := 0; i < scanLimit; i++ {
		svc.Limiter().Hit(svc.Now(), "scan:"+current.DeviceID, scanLimit, scanWindow)
	}
	expectCode(t, take2(svc.IngestScans(ctx, current, []ScanInput{{ID: "over", Heartbeat: true}}, 0)), CodeRateLimited)
	if err := svc.RevokeDevice(ctx, ada.ID, house, hub.DeviceID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AuthenticateDevice(ctx, next.Token)), CodeDeviceRevoked)

	var gone *presence
	gone.markOffline("x")
	gone.setQueued("x", 1)
	if gone.touch("x", time.Now()) {
		t.Fatal("nil presence wrote")
	}
	if _, ok := gone.last("x"); ok {
		t.Fatal("nil presence remembered")
	}
	if _, ok := gone.queue("x"); ok {
		t.Fatal("nil presence queued")
	}
	if svc.presence.touch("", time.Now()) {
		t.Fatal("empty id touched")
	}
}

func wallClock() time.Time { return time.Now().UTC() }

func take2[T any](_ T, err error) error { return err }

func expectCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("got %v want %s", err, code)
	}
}
