package auth

import (
	"context"
	"testing"
)

func TestAdminIssueRecoverRequiresAdminAccount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openAuthDB(t)
	svc := NewService(db, dir)
	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RedeemSetup(ctx, code, "Member Only", "member@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET operator = 0 WHERE email_norm = ?`, "member@pantry.test"); err != nil {
		t.Fatal(err)
	}
	err = svc.AdminIssue(ctx, true)
	if err == nil || err.Error() != "There is no admin account to recover." {
		t.Fatalf("AdminIssue recover = %v", err)
	}
}
