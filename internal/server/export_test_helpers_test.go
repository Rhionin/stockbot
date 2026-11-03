package server

import (
	"testing"
)

func TestSeedDefaultExportAppInsertsCredentials(t *testing.T) {
	_, env := setupTestWithDB(t)
	seedDefaultExportApp(t, env.DB, "push-test")
	var n int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM provider_app_credentials WHERE provider_id = 'push-test'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}
