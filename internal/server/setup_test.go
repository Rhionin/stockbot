package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/restock"
	_ "modernc.org/sqlite"
)

// testProductCacheTTL is a test-friendly TTL for the Refresher constructed by
// setupTestWithDB. Tests that need a row to be fresh or stale move env.Clock
// rather than waiting on this value.
const testProductCacheTTL = 30 * 24 * time.Hour

// setupTestDB opens an in-memory SQLite database, applies all migrations, and returns the connection.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openMigratedDB(t, ":memory:")
}

// pantryTestDSN matches the server file: WAL, so a read can run beside a
// writer. An in-memory database stays as it is. A DSN that already names
// the journal is left alone.
func pantryTestDSN(dsn string) string {
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:") || strings.Contains(dsn, "journal_mode") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&_pragma=journal_mode(WAL)"
	}
	return dsn + "?_pragma=journal_mode(WAL)"
}

func openMigratedDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	conn, err := app.OpenSQLite(pantryTestDSN(dsn))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	// A :memory: database lives on one connection. A second pooled connection
	// is an empty database, so transactions (group suggestion refresh during
	// lookup) would miss the migrated schema. The server uses one connection
	// too, so a file-backed test matches that pool.
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return conn
}

// setupTestWithDB creates a fully configured HTTP handler and returns it along
// with a fully populated testEnv. The returned Refresher, fakeClock, and fakeUpstream
// are the same instances wired into the handler's LookupService, so a test (or exchanges())
// that needs to build a second handler against the same state must reuse them
// rather than constructing fresh ones — a second Refresher would own its own
// WaitGroup and in-flight map, and env.Refresher.Wait() would return without
// awaiting goroutines the second handler started.
func setupTestWithDB(t *testing.T) (http.Handler, testEnv) {
	t.Helper()
	return setupTestWithContributor(t, nil)
}

func setupTestWithContributor(t *testing.T, contributor product.UpstreamContributor, extra ...Option) (http.Handler, testEnv) {
	t.Helper()
	return setupOnDB(t, setupTestDB(t), contributor, extra...)
}

func setupTestOnDSN(t *testing.T, dsn string, contributor product.UpstreamContributor, extra ...Option) (http.Handler, testEnv) {
	t.Helper()
	return setupOnDB(t, openMigratedDB(t, dsn), contributor, extra...)
}

func setupOnDB(t *testing.T, db *sql.DB, contributor product.UpstreamContributor, extra ...Option) (http.Handler, testEnv) {
	t.Helper()
	productRepo := product.NewCatalog(db)
	clock := newFakeClock(time.Now())

	// Build one fakeProductOpener per database
	databases := make(map[product.ExternalSource]*fakeProductOpener)
	for _, source := range []product.ExternalSource{
		product.ExternalSourceOpenFoodFacts,
		product.ExternalSourceOpenProductsFacts,
		product.ExternalSourceOpenBeautyFacts,
		product.ExternalSourceOpenPetFoodFacts,
	} {
		databases[source] = newFakeProductOpener()
	}

	// Build fakeClients map to hold fakeProductOpener instances
	fakeClients := make(map[product.ExternalSource]product.BarcodeLookup)
	for source, fake := range databases {
		fakeClients[source] = fake
	}

	externalLookup := product.NewExternalLookup(fakeClients)
	upstream := &fakeUpstream{
		ExternalLookup: externalLookup,
		databases:      databases,
	}

	restockTargets := restock.NewTargets(db)
	refresher := &product.Refresher{
		Catalog:               productRepo,
		Upstream:              upstream,
		TTL:                   testProductCacheTTL,
		ExternalLookupEnabled: true,
		Now:                   clock.Now,
		Tags:                  restockTargets,
	}
	lookupService := &product.LookupService{
		Catalog:   productRepo,
		Upstream:  upstream,
		Refresher: refresher,
		Now:       clock.Now,
		MissTTL:   5 * time.Minute,
		Tags:      restockTargets,
	}

	registry := cart.NewRegistry()
	opts := []Option{WithCartRegistry(registry, cart.NewLedger(db))}
	if contributor != nil {
		opts = append(opts, WithContributor(contributor))
	}
	opts = append(opts, extra...)
	handler, _ := NewHandler(productRepo, lookupService, refresher, db, opts...)

	// Get the Open Food Facts fake for backward compatibility
	offFake := databases[product.ExternalSourceOpenFoodFacts]

	missTTL := 5 * time.Minute

	env := testEnv{
		T:             t,
		DB:            db,
		ProductStore:  productRepo,
		Upstream:      upstream,
		OpenFoodFacts: offFake,
		Refresher:     refresher,
		Clock:         clock,
		MissTTL:       missTTL,
		Contributor:   contributor,
		Registry:      registry,
	}

	return handler, env
}

func setupProduct(id, name, category string) func(env testEnv) {
	return func(env testEnv) {
		prod := product.Product{ID: id, Name: name, Category: category}
		if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
			env.T.Fatalf("failed to create product: %v", err)
		}
	}
}

func setupProductWithBarcode(id, name, category, barcode string) func(env testEnv) {
	return func(env testEnv) {
		prod := product.Product{ID: id, Name: name, Category: category}
		if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
			env.T.Fatalf("failed to create product: %v", err)
		}
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, id, "global", ""); err != nil {
			env.T.Fatalf("failed to create barcode mapping: %v", err)
		}
	}
}

// setupExternalProduct creates a source = 'external' product row, the kind
// the Refresher is willing to revalidate. refreshedAt nil leaves the row
// never-checked (stale); a non-nil value stamps it explicitly.
func setupExternalProduct(id, name, category string, refreshedAt *time.Time) func(env testEnv) {
	return func(env testEnv) {
		prod := product.Product{
			ID:          id,
			Name:        name,
			Category:    category,
			Source:      product.SourceExternal,
			RefreshedAt: refreshedAt,
		}
		if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
			env.T.Fatalf("failed to create external product: %v", err)
		}
	}
}

func setupMultipleProducts(products []product.Product) func(env testEnv) {
	return func(env testEnv) {
		for _, p := range products {
			if err := env.ProductStore.CreateProduct(context.Background(), p); err != nil {
				env.T.Fatalf("failed to create product: %v", err)
			}
		}
	}
}
