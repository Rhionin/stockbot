package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupDisplayNamesPublicSetupPath(t *testing.T) {
	ctx := context.Background()
	db := openAuthDB(t)
	dir := t.TempDir()
	svc := NewService(db, dir)

	if err := svc.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	assertPublicSetupDisplay(t, dir)

	if err := svc.AdminIssue(ctx, false); err != nil {
		t.Fatal(err)
	}
	assertPublicSetupDisplay(t, dir)

	code, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RedeemSetup(ctx, code, "Ada", "ada@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdminIssue(ctx, true); err != nil {
		t.Fatal(err)
	}
	assertPublicSetupDisplay(t, dir)
}

func assertPublicSetupDisplay(t *testing.T, dir string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "code"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "/setup") {
		t.Fatalf("display file missing /setup: %s", text)
	}
	if strings.Contains(text, "http://pi:8080") {
		t.Fatalf("display file still names http://pi:8080: %s", text)
	}
}
