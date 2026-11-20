package server

import (
	"context"
	"sync"

	"github.com/Rhionin/stockbot/internal/product"
)

// fakeProductOpener implements the barcode lookup interface for testing.
// It maintains an in-memory store of products keyed by barcode, simulating an upstream database.
//
// Background revalidation goroutines call LookupBarcode concurrently with the test body, so every
// map is guarded by mu.
type fakeProductOpener struct {
	mu       sync.Mutex
	store    map[string]*product.ProductSummary
	errs     map[string]error
	calls    map[string]int
	blocking map[string]<-chan struct{}
}

// newFakeProductOpener creates an empty fake upstream client.
// At some future point, we can seed this with realistic test data.
func newFakeProductOpener() *fakeProductOpener {
	return &fakeProductOpener{
		store:    make(map[string]*product.ProductSummary),
		errs:     make(map[string]error),
		calls:    make(map[string]int),
		blocking: make(map[string]<-chan struct{}),
	}
}

// LookupBarcode looks up a product by barcode in the in-memory store.
// Returns ErrProductNotFound if the barcode is not in the store.
//
// Precedence: a registered blocking channel is waited on first, then a seeded error is
// returned if present, then the store is consulted. Every call is counted, including
// blocked, error, and not-found calls.
func (f *fakeProductOpener) LookupBarcode(ctx context.Context, barcode string) (*product.ProductSummary, error) {
	f.mu.Lock()
	f.calls[barcode]++
	release := f.blocking[barcode]
	f.mu.Unlock()

	if release != nil {
		<-release
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if err, ok := f.errs[barcode]; ok {
		return nil, err
	}

	prod, ok := f.store[barcode]
	if !ok {
		return nil, product.ErrProductNotFound
	}
	return prod, nil
}

// Seed adds a product to the fake's in-memory store for the given barcode.
// This allows tests to simulate products existing in an upstream database.
//
// Seed overwrites any existing entry for barcode, and LookupBarcode reads the map at call
// time. Re-seeding a barcode between the cache fill and the refresh is how tests make the
// upstream answer differ from what was cached.
func (f *fakeProductOpener) Seed(barcode string, prod *product.ProductSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[barcode] = prod
}

// SeedError records an error to return for barcode instead of a product. This is distinct
// from an absent barcode, which keeps yielding ErrProductNotFound.
func (f *fakeProductOpener) SeedError(barcode string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[barcode] = err
}

// SeedBlocking makes LookupBarcode for barcode block until release is closed or receives a
// value, before falling through to the seeded error or store lookup. This lets tests hold a
// call open for concurrency assertions.
func (f *fakeProductOpener) SeedBlocking(barcode string, release <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocking[barcode] = release
}

// CallCount returns the number of times LookupBarcode has been called for barcode.
func (f *fakeProductOpener) CallCount(barcode string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[barcode]
}

// TotalCalls returns the number of LookupBarcode calls across every barcode.
func (f *fakeProductOpener) TotalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.calls {
		total += n
	}
	return total
}

// LookupIn looks up a product in a specific database. For the fake, we ignore
// the source parameter and delegate to LookupBarcode since this is a test double.
func (f *fakeProductOpener) LookupIn(ctx context.Context, source product.ExternalSource, barcode string) (*product.ProductSummary, error) {
	return f.LookupBarcode(ctx, barcode)
}

// Lookup queries all databases concurrently and returns a FanOutResult.
// For the fake, we return a hit if LookupBarcode finds the product, or a confirmed miss
// if it returns ErrProductNotFound, or unresolved on other errors.
func (f *fakeProductOpener) Lookup(ctx context.Context, barcode string) product.FanOutResult {
	prod, err := f.LookupBarcode(ctx, barcode)
	if err != nil {
		if err == product.ErrProductNotFound {
			return product.FanOutResult{Outcome: product.FanOutConfirmedMiss}
		}
		return product.FanOutResult{Outcome: product.FanOutUnresolved}
	}
	return product.FanOutResult{
		Outcome: product.FanOutHit,
		Product: prod,
		Source:  product.ExternalSourceOpenFoodFacts,
	}
}
