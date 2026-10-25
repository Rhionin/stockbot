package product

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestSaveBrandRoundTrip(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := NewCatalog(conn)
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	created, err := catalog.CreateTypedProduct(ctx, scope, Product{Name: "Crackers"})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveBrand(ctx, scope, created.ID, "Pantry"); err != nil {
		t.Fatal(err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "pantry", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Brand != "Pantry" {
		t.Fatalf("hits = %+v", hits)
	}
}
