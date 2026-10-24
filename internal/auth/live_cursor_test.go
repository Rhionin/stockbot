package auth

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/changes"
)

func TestDeviceRenameAndRevokeAdvanceTheDevicesCursor(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	svc := NewService(db, t.TempDir())
	primary, err := PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	house := primary.HouseholdID()
	const owner = "user-alpha"
	now := "2026-04-04T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Owner Alpha', 'alpha@example.test', 'hash', 'local', 1, ?)`, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'owner', ?)`, house, owner, now); err != nil {
		t.Fatal(err)
	}
	const device = "device-counter"
	if _, err := db.Exec(`
		INSERT INTO devices (id, household_id, kind, label, created_at)
		VALUES (?, ?, 'pi_hub', 'Counter', ?)`, device, house, now); err != nil {
		t.Fatal(err)
	}
	before, err := changes.Version(ctx, db, house, changes.ScopeDevices)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenameDevice(ctx, owner, house, device, "North counter"); err != nil {
		t.Fatal(err)
	}
	renamed, err := changes.Version(ctx, db, house, changes.ScopeDevices)
	if err != nil {
		t.Fatal(err)
	}
	if renamed <= before {
		t.Fatalf("rename version %d, before %d", renamed, before)
	}
	if err := svc.RevokeDevice(ctx, owner, house, device); err != nil {
		t.Fatal(err)
	}
	revoked, err := changes.Version(ctx, db, house, changes.ScopeDevices)
	if err != nil {
		t.Fatal(err)
	}
	if revoked <= renamed {
		t.Fatalf("revoke version %d, renamed %d", revoked, renamed)
	}
}
