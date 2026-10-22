package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/grocysync"
	"github.com/Rhionin/stockbot/internal/syncsnap"
)

func TestRunBackfillWritesOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(syncsnap.Snapshot{
			Moves: []syncsnap.Move{{
				ID: "m1", ProductID: "p1", Direction: "out", Quantity: 1,
				At: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
			}},
		})
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "grocy.db")
	db, err := app.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE stock_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		product_id INTEGER NOT NULL,
		amount REAL NOT NULL,
		stock_id TEXT NOT NULL,
		transaction_id TEXT NOT NULL,
		transaction_type TEXT NOT NULL,
		undone INTEGER NOT NULL DEFAULT 0,
		note TEXT,
		row_created_timestamp TEXT,
		user_id INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	mapPath := filepath.Join(t.TempDir(), "map.db")
	m, err := grocysync.OpenMap(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Put(grocysync.Link{PantryType: "product", PantryID: "p1", GrocyType: "products", GrocyID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(grocysync.Link{PantryType: "location", PantryID: "Pantry", GrocyType: "locations", GrocyID: 4}); err != nil {
		t.Fatal(err)
	}
	m.Close()

	cfg := grocysync.Config{
		PantryURL:   srv.URL,
		PantryToken: "0123456789abcdef",
		GrocyURL:    "http://grocy",
		GrocyKey:    "key",
		MapPath:     mapPath,
		Loc:         time.UTC,
	}
	if err := runBackfill(cfg, []string{"--nope"}); err == nil {
		t.Fatal("expected a usage error")
	}
	if err := runBackfill(cfg, []string{"--db", dbPath}); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("confirm gate: %v", err)
	}
	if err := runBackfill(grocysync.Config{}, []string{"--db", dbPath, "--confirm"}); err == nil {
		t.Fatal("expected missing configuration to refuse")
	}
	if err := runBackfill(cfg, []string{"--db", dbPath, "--confirm"}); err != nil {
		t.Fatal(err)
	}
	if err := runBackfill(cfg, []string{"--db", dbPath, "--confirm"}); err != nil {
		t.Fatal(err)
	}
	check, err := app.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var amount float64
	var stamp string
	if err := check.QueryRow(`SELECT amount, row_created_timestamp FROM stock_log`).Scan(&amount, &stamp); err != nil {
		t.Fatal(err)
	}
	if amount != -1 || stamp != "2024-01-02 03:04:05" {
		t.Fatalf("journal = %v %s", amount, stamp)
	}
}
