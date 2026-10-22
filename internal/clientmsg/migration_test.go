package clientmsg

import "testing"

func TestMigrationUpdatingPinned(t *testing.T) {
	const want = "Stockbot is updating. This takes about a minute."
	if MigrationUpdating != want {
		t.Fatalf("MigrationUpdating = %q, want %q", MigrationUpdating, want)
	}
}
