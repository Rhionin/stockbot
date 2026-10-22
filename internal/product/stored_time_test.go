package product

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestRefreshAndMissClocksStoreCleanUTC(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(db)
	if err := catalog.CreateProduct(context.Background(), Product{ID: "prod", Name: "Oats"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := catalog.MarkRefreshed(ctx, "prod", (&Refresher{}).now()); err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordBarcodeMiss(ctx, "000111222333", (&LookupService{}).now()); err != nil {
		t.Fatal(err)
	}
	var refreshed, checked string
	if err := db.QueryRow(`SELECT CAST(refreshed_at AS TEXT) FROM products WHERE id = 'prod'`).Scan(&refreshed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT CAST(checked_at AS TEXT) FROM barcode_misses WHERE barcode = '000111222333'`).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{refreshed, checked} {
		if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "m=") {
			t.Fatalf("stored %q", raw)
		}
	}
}

func TestSuffixedRefreshAndMissReadBack(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	const stored = "2026-10-03 18:12:53.123456789 +0000 UTC m=+12.5"
	want := time.Date(2026, 10, 3, 18, 12, 53, 123456789, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO products (id, name, refreshed_at) VALUES ('prod', 'Oats', ?);
		INSERT INTO barcode_misses (barcode, checked_at) VALUES ('000111222333', ?)`, stored, stored); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(db)
	ctx := context.Background()
	got, err := catalog.GetProductByID(ctx, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshedAt == nil || !got.RefreshedAt.Equal(want) {
		t.Fatalf("refreshed_at = %v", got.RefreshedAt)
	}
	miss, err := catalog.GetBarcodeMiss(ctx, "000111222333")
	if err != nil {
		t.Fatal(err)
	}
	if miss == nil || !miss.Equal(want) {
		t.Fatalf("checked_at = %v", miss)
	}
}
