package auth

import (
	"testing"
	"time"
)

func TestRevokeDeviceForeignIDPrecedesRole(t *testing.T) {
	ctx, svc, ownerID, alder := bootDeviceAPI(t)
	start := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })
	birch := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Birch', ?, 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 0)`, birch, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'owner', ?)`, birch, ownerID, stamp(start)); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.StartPairing(ctx, ownerID, birch)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "birch-scanner-q7", "pi_hub", "1.0.0", "boot-birch")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	member := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Alder Member', 'alder-member@pantry.test', ?, 'local', 0, ?)`, member, hash, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'member', ?)`, alder, member, stamp(start)); err != nil {
		t.Fatal(err)
	}
	alderPair, err := svc.StartPairing(ctx, ownerID, alder)
	if err != nil {
		t.Fatal(err)
	}
	alderPaired, err := svc.CompletePairing(ctx, alderPair.Code, "alder-scanner-q7", "pi_hub", "1.0.0", "boot-alder")
	if err != nil {
		t.Fatal(err)
	}

	expectCode(t, svc.RevokeDevice(ctx, member, alder, paired.DeviceID), CodeNotFound)
	expectCode(t, svc.RevokeDevice(ctx, ownerID, alder, paired.DeviceID), CodeNotFound)
	unknownID := newID()
	expectCode(t, svc.RevokeDevice(ctx, ownerID, alder, unknownID), CodeNotFound)
	expectCode(t, svc.RevokeDevice(ctx, member, alder, alderPaired.DeviceID), CodeForbiddenRole)
	if err := svc.RevokeDevice(ctx, ownerID, alder, alderPaired.DeviceID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AuthenticateDevice(ctx, alderPaired.Token)), CodeDeviceRevoked)
}
