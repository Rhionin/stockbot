package auth

import (
	"strings"
	"testing"
	"time"
)

func TestCreateResetLinkRejectsForeignHouseholdMember(t *testing.T) {
	ctx, svc, alderOwner, alder := bootDeviceAPI(t)
	start := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	birch := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Birch', ?, 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 0)`, birch, stamp(start)); err != nil {
		t.Fatal(err)
	}

	hash, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	birchMember := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Birch Member', 'birch-member-q646@pantry.test', ?, 'local', 0, ?)`,
		birchMember, hash, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'member', ?)`, birch, birchMember, stamp(start)); err != nil {
		t.Fatal(err)
	}

	var linksBefore int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM reset_links WHERE user_id = ?`, birchMember).Scan(&linksBefore); err != nil {
		t.Fatal(err)
	}

	expectCode(t, take2(svc.CreateResetLink(ctx, alderOwner, alder, birchMember, false)), CodeNotFound)

	var linksAfter int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM reset_links WHERE user_id = ?`, birchMember).Scan(&linksAfter); err != nil {
		t.Fatal(err)
	}
	if linksAfter != linksBefore {
		t.Fatalf("reset_links for birch member %d -> %d", linksBefore, linksAfter)
	}

	if _, err := svc.LoginAccount(ctx, "birch-member-q646@pantry.test", "correct horse", ""); err != nil {
		t.Fatalf("birch member login after rejected reset: %v", err)
	}

	// Alder-only member: owner can still mint a reset (control).
	alderHash, err := HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	alderOnly := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Alder Member', 'alder-member-q646@pantry.test', ?, 'local', 0, ?)`,
		alderOnly, alderHash, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'member', ?)`, alder, alderOnly, stamp(start)); err != nil {
		t.Fatal(err)
	}
	link, err := svc.CreateResetLink(ctx, alderOwner, alder, alderOnly, false)
	if err != nil || link.Code == "" || !strings.HasPrefix(link.Code, "sbr_") {
		t.Fatalf("same-household reset %#v %v", link, err)
	}
}