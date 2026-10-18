package product

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

// --------------------------------------------------------------------------
// mergeRefresh
// --------------------------------------------------------------------------

// Feature: product-cache-freshness, Property 7: A merge takes an upstream field only when upstream supplied one
//
// Validates: Requirements 3.1, 3.2, 3.3, 5.1, 5.3
//
// For any cached row and any Open Food Facts response, each of category,
// unit_of_measure, and image_url equals the upstream value when non-empty and
// the prior cached value otherwise; name additionally requires
// !cached.NameOverridden to take the upstream value. An all-empty upstream
// (the "not found" / "request failed" shape) leaves every field at its prior
// cached value.
func TestMergeRefresh_UpstreamFieldOnlyWhenSupplied(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nonEmpty := rapid.StringMatching(`[A-Za-z0-9 ]{1,20}`)
		maybeEmpty := func(label string) string {
			if rapid.Bool().Draw(rt, label+"Present") {
				return nonEmpty.Draw(rt, label)
			}
			return ""
		}

		cached := Product{
			ID:             "prod-1",
			Name:           nonEmpty.Draw(rt, "cachedName"),
			Category:       nonEmpty.Draw(rt, "cachedCategory"),
			UnitOfMeasure:  nonEmpty.Draw(rt, "cachedUnit"),
			ImageURL:       nonEmpty.Draw(rt, "cachedImage"),
			CreatedAt:      time.Now(),
			Source:         SourceExternal,
			NameOverridden: rapid.Bool().Draw(rt, "nameOverridden"),
		}
		upstream := ProductSummary{
			ID:            cached.ID,
			Name:          maybeEmpty("upstreamName"),
			Category:      maybeEmpty("upstreamCategory"),
			UnitOfMeasure: maybeEmpty("upstreamUnit"),
			ImageURL:      maybeEmpty("upstreamImage"),
		}

		merged := mergeRefresh(cached, upstream)

		wantName := cached.Name
		if upstream.Name != "" && !cached.NameOverridden {
			wantName = upstream.Name
		}
		if merged.Name != wantName {
			rt.Fatalf("Name: want %q, got %q (cached=%q upstream=%q overridden=%v)",
				wantName, merged.Name, cached.Name, upstream.Name, cached.NameOverridden)
		}

		wantCategory := cached.Category
		if upstream.Category != "" {
			wantCategory = upstream.Category
		}
		if merged.Category != wantCategory {
			rt.Fatalf("Category: want %q, got %q", wantCategory, merged.Category)
		}

		wantUnit := cached.UnitOfMeasure
		if upstream.UnitOfMeasure != "" {
			wantUnit = upstream.UnitOfMeasure
		}
		if merged.UnitOfMeasure != wantUnit {
			rt.Fatalf("UnitOfMeasure: want %q, got %q", wantUnit, merged.UnitOfMeasure)
		}

		wantImage := cached.ImageURL
		if upstream.ImageURL != "" {
			wantImage = upstream.ImageURL
		}
		if merged.ImageURL != wantImage {
			rt.Fatalf("ImageURL: want %q, got %q", wantImage, merged.ImageURL)
		}

		// id, source, created_at, and name_overridden are never touched.
		if merged.ID != cached.ID {
			rt.Fatalf("ID: want %q, got %q", cached.ID, merged.ID)
		}
		if merged.Source != cached.Source {
			rt.Fatalf("Source: want %q, got %q", cached.Source, merged.Source)
		}
		if !merged.CreatedAt.Equal(cached.CreatedAt) {
			rt.Fatalf("CreatedAt: want %v, got %v", cached.CreatedAt, merged.CreatedAt)
		}
		if merged.NameOverridden != cached.NameOverridden {
			rt.Fatalf("NameOverridden: want %v, got %v", cached.NameOverridden, merged.NameOverridden)
		}
	})
}

func TestMergeRefreshSetsBrandFromUpstream(t *testing.T) {
	cached := Product{ID: "prod-1", Name: "N", Brand: "Old", Source: SourceExternal}
	got := mergeRefresh(cached, ProductSummary{Name: "N", Brand: "Tillamook"})
	if got.Brand != "Tillamook" {
		t.Fatalf("brand = %q, want Tillamook", got.Brand)
	}
	kept := mergeRefresh(cached, ProductSummary{Name: "N"})
	if kept.Brand != "Old" {
		t.Fatalf("brand = %q, want the recorded brand kept", kept.Brand)
	}
}

// TestMergeRefresh_FieldPresenceTable enumerates all 16 combinations of which
// of the four upstream fields are present, including the all-empty case (the
// shape of a not-found or failed upstream response). NameOverridden is false
// throughout, since the name/NameOverridden interaction is covered by
// TestMergeRefresh_UpstreamFieldOnlyWhenSupplied above.
func TestMergeRefresh_FieldPresenceTable(t *testing.T) {
	cached := Product{
		ID:            "prod-1",
		Name:          "Old Name",
		Category:      "Old Category",
		UnitOfMeasure: "Old Unit",
		ImageURL:      "Old Image",
		Source:        SourceExternal,
	}

	for mask := 0; mask < 16; mask++ {
		nameSet := mask&1 != 0
		categorySet := mask&2 != 0
		unitSet := mask&4 != 0
		imageSet := mask&8 != 0

		name := ""
		if nameSet {
			name = "New Name"
		}
		category := ""
		if categorySet {
			category = "New Category"
		}
		unit := ""
		if unitSet {
			unit = "New Unit"
		}
		image := ""
		if imageSet {
			image = "New Image"
		}

		t.Run(fieldPresenceCaseName(nameSet, categorySet, unitSet, imageSet), func(t *testing.T) {
			upstream := ProductSummary{ID: cached.ID, Name: name, Category: category, UnitOfMeasure: unit, ImageURL: image}
			merged := mergeRefresh(cached, upstream)

			wantName, wantCategory, wantUnit, wantImage := cached.Name, cached.Category, cached.UnitOfMeasure, cached.ImageURL
			if nameSet {
				wantName = name
			}
			if categorySet {
				wantCategory = category
			}
			if unitSet {
				wantUnit = unit
			}
			if imageSet {
				wantImage = image
			}

			if merged.Name != wantName {
				t.Errorf("Name: want %q, got %q", wantName, merged.Name)
			}
			if merged.Category != wantCategory {
				t.Errorf("Category: want %q, got %q", wantCategory, merged.Category)
			}
			if merged.UnitOfMeasure != wantUnit {
				t.Errorf("UnitOfMeasure: want %q, got %q", wantUnit, merged.UnitOfMeasure)
			}
			if merged.ImageURL != wantImage {
				t.Errorf("ImageURL: want %q, got %q", wantImage, merged.ImageURL)
			}
		})
	}
}

// fieldPresenceCaseName builds a readable subtest name from which fields are
// present in the upstream response, e.g. "name,category" or "all-empty".
func fieldPresenceCaseName(nameSet, categorySet, unitSet, imageSet bool) string {
	name := ""
	add := func(present bool, label string) {
		if !present {
			return
		}
		if name != "" {
			name += ","
		}
		name += label
	}
	add(nameSet, "name")
	add(categorySet, "category")
	add(unitSet, "unit")
	add(imageSet, "image")
	if name == "" {
		return "all-empty"
	}
	return name
}

// --------------------------------------------------------------------------
// classify
// --------------------------------------------------------------------------

// Feature: product-cache-freshness, Property 12: The outcome classifies the field delta
//
// Validates: Requirements 4.4, 4.5
//
// classify reports OutcomeUpdated when any of the four merged fields differs
// from its pre-refresh value, and OutcomeUnchanged when all four are equal —
// refreshed_at moving never by itself counts as an update.
func TestClassify(t *testing.T) {
	base := Product{
		ID:            "prod-1",
		Name:          "Name",
		Category:      "Category",
		UnitOfMeasure: "Unit",
		ImageURL:      "Image",
	}

	tests := []struct {
		name   string
		after  Product
		expect RefreshOutcome
	}{
		{
			name:   "name differs",
			after:  withField(base, "Name", "New Name"),
			expect: OutcomeUpdated,
		},
		{
			name:   "category differs",
			after:  withField(base, "Category", "New Category"),
			expect: OutcomeUpdated,
		},
		{
			name:   "unit_of_measure differs",
			after:  withField(base, "UnitOfMeasure", "New Unit"),
			expect: OutcomeUpdated,
		},
		{
			name:   "image_url differs",
			after:  withField(base, "ImageURL", "New Image"),
			expect: OutcomeUpdated,
		},
		{
			name:   "brand differs",
			after:  withField(base, "Brand", "Tillamook"),
			expect: OutcomeUpdated,
		},
		{
			name:   "all four equal",
			after:  base,
			expect: OutcomeUnchanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(base, tt.after)
			if got != tt.expect {
				t.Errorf("classify: want %q, got %q", tt.expect, got)
			}
		})
	}
}

// withField returns a copy of p with the named field replaced, used by
// TestClassify to build each before/after pair off a single base value.
func withField(p Product, field, value string) Product {
	switch field {
	case "Name":
		p.Name = value
	case "Category":
		p.Category = value
	case "UnitOfMeasure":
		p.UnitOfMeasure = value
	case "ImageURL":
		p.ImageURL = value
	case "Brand":
		p.Brand = value
	}
	return p
}

// TestClassify_RefreshedAtIgnored confirms refreshed_at moving is never by
// itself an update: classify only compares the four field values, so a
// before/after pair that differs only in RefreshedAt is unchanged.
func TestClassify_RefreshedAtIgnored(t *testing.T) {
	before := Product{ID: "prod-1", Name: "Name", Category: "Category", UnitOfMeasure: "Unit", ImageURL: "Image"}
	then := time.Now()
	after := before
	after.RefreshedAt = &then

	if got := classify(before, after); got != OutcomeUnchanged {
		t.Errorf("classify: want %q, got %q", OutcomeUnchanged, got)
	}
}

// --------------------------------------------------------------------------
// isStale
// --------------------------------------------------------------------------

// Feature: product-cache-freshness, Property 3: Staleness is exactly "never checked, or older than the TTL"
//
// Validates: Requirements 1.5
//
// isStale reports true when RefreshedAt is nil, and otherwise true exactly
// when the elapsed time since RefreshedAt exceeds the TTL. The exactly-at-TTL
// boundary is only reachable by calling the predicate directly: no HTTP
// exchange can pin the clock to that instant.
func TestIsStale(t *testing.T) {
	const ttl = 24 * time.Hour
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	refresher := &Refresher{TTL: ttl, Now: func() time.Time { return now }}

	insideTTL := now.Add(-1 * time.Hour)
	outsideTTL := now.Add(-25 * time.Hour)
	exactlyAtTTL := now.Add(-ttl)

	tests := []struct {
		name        string
		refreshedAt *time.Time
		want        bool
	}{
		{name: "nil refreshed_at is stale", refreshedAt: nil, want: true},
		{name: "inside the TTL is not stale", refreshedAt: &insideTTL, want: false},
		{name: "outside the TTL is stale", refreshedAt: &outsideTTL, want: true},
		{name: "exactly at the TTL is not stale", refreshedAt: &exactlyAtTTL, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Product{ID: "prod-1", Source: SourceExternal, RefreshedAt: tt.refreshedAt}
			if got := refresher.isStale(p); got != tt.want {
				t.Errorf("isStale: want %v, got %v", tt.want, got)
			}
		})
	}
}

// --------------------------------------------------------------------------
// ScheduleRefresh / Wait: single-flight and goroutine lifetime
// --------------------------------------------------------------------------

// fakeRefreshCatalog is a minimal, mutex-guarded Catalog fake satisfying
// Refresher's inline Catalog interface. ScheduleRefresh's synchronous gate
// and the background goroutine it spawns both call GetProductByID, so this
// fake must tolerate concurrent access.
type fakeRefreshCatalog struct {
	mu       sync.Mutex
	products map[string]Product
}

func newFakeRefreshCatalog(seed Product) *fakeRefreshCatalog {
	return &fakeRefreshCatalog{products: map[string]Product{seed.ID: seed}}
}

func (c *fakeRefreshCatalog) GetProductByID(ctx context.Context, id string) (*Product, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.products[id]
	if !ok {
		return nil, nil
	}
	cp := p
	return &cp, nil
}

func (c *fakeRefreshCatalog) SaveRefresh(ctx context.Context, p Product, refreshedAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.products[p.ID]
	if !ok {
		return fmt.Errorf("SaveRefresh: product %q not found", p.ID)
	}
	existing.Name = p.Name
	existing.Category = p.Category
	existing.UnitOfMeasure = p.UnitOfMeasure
	existing.ImageURL = p.ImageURL
	at := refreshedAt
	existing.RefreshedAt = &at
	c.products[p.ID] = existing
	return nil
}

func (c *fakeRefreshCatalog) MarkRefreshed(ctx context.Context, id string, refreshedAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.products[id]
	if !ok {
		return fmt.Errorf("MarkRefreshed: product %q not found", id)
	}
	at := refreshedAt
	existing.RefreshedAt = &at
	c.products[id] = existing
	return nil
}

func newRefreshCatalog(t *testing.T) *Catalog {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return NewCatalog(conn)
}

// blockingOpenFoodFacts is a fake OpenFoodFacts client whose LookupBarcode
// blocks until unblock is called, and counts calls per barcode. The block is
// what distinguishes "the in-flight guard rejected the duplicates" from
// "the guard merely serialized them": a non-blocking fake would let
// duplicate calls run one after another and still land on a call count of 1
// for the wrong reason (nothing else is trying to call concurrently once
// each prior call has already returned).
type blockingOpenFoodFacts struct {
	mu      sync.Mutex
	calls   map[string]int
	release chan struct{}
	result  ProductSummary
}

func newBlockingOpenFoodFacts(result ProductSummary) *blockingOpenFoodFacts {
	return &blockingOpenFoodFacts{calls: make(map[string]int), release: make(chan struct{}), result: result}
}

func (f *blockingOpenFoodFacts) LookupBarcode(ctx context.Context, barcode string) (*ProductSummary, error) {
	f.mu.Lock()
	f.calls[barcode]++
	release := f.release
	f.mu.Unlock()

	<-release

	result := f.result
	result.ID = barcode
	return &result, nil
}

func (f *blockingOpenFoodFacts) LookupIn(ctx context.Context, source ExternalSource, barcode string) (*ProductSummary, error) {
	return f.LookupBarcode(ctx, barcode)
}

func (f *blockingOpenFoodFacts) CallCount(barcode string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[barcode]
}

// reopen replaces the release channel with a fresh, unclosed one so a
// subsequent LookupBarcode call blocks again.
func (f *blockingOpenFoodFacts) reopen() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.release = make(chan struct{})
}

// unblock releases every call currently blocked in LookupBarcode.
func (f *blockingOpenFoodFacts) unblock() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.release)
}

// assertNoLeakedGoroutines polls runtime.NumGoroutine until it settles back
// to baseline or a short deadline passes. Wait() only guarantees the
// WaitGroup counter reached zero, which happens inside the deferred cleanup
// a background goroutine runs just before it returns, so a brief poll (rather
// than a single instantaneous check) is needed to avoid a false failure on
// the last few instructions of that goroutine's return.
func assertNoLeakedGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if current := runtime.NumGoroutine(); current <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: baseline %d, current %d", baseline, runtime.NumGoroutine())
		}
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
}

// Feature: product-cache-freshness, Property 6: Concurrent revalidations of one barcode collapse to one upstream request
//
// Validates: Requirements 2.5, 8.3, 8.4
//
// N concurrent ScheduleRefresh calls for the same stale external row, against
// an upstream held open on a release channel, produce exactly one upstream
// call once released and awaited. Wait leaves no goroutine the Refresher
// started running, and the in-flight entry it tracked is released: a second
// ScheduleRefresh call after Wait is accepted and reaches the upstream.
func TestScheduleRefresh_ConcurrentCallsCollapseToOneUpstreamRequest(t *testing.T) {
	const productID = "012345678905"

	catalog := newFakeRefreshCatalog(Product{
		ID:       productID,
		Name:     "Old Name",
		Category: "Old Category",
		Source:   SourceExternal,
		// RefreshedAt is nil, so the row starts stale.
	})
	off := newBlockingOpenFoodFacts(ProductSummary{Name: "New Name", Category: "New Category"})

	refresher := &Refresher{
		Catalog:               catalog,
		Upstream:              off,
		TTL:                   0, // any elapsed time makes the row stale again after a refresh
		ExternalLookupEnabled: true,
	}

	baseline := runtime.NumGoroutine()

	const n = 8
	var launched sync.WaitGroup
	launched.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer launched.Done()
			refresher.ScheduleRefresh(context.Background(), productID)
		}()
	}
	// Every ScheduleRefresh call has returned. Exactly one of them won the
	// in-flight guard and spawned a background goroutine, which is now
	// blocked inside LookupBarcode; the other n-1 saw the entry already in
	// flight and returned without spawning anything.
	launched.Wait()

	off.unblock()
	refresher.Wait()

	if got := off.CallCount(productID); got != 1 {
		t.Fatalf("upstream call count for %q: want 1, got %d", productID, got)
	}

	assertNoLeakedGoroutines(t, baseline)

	// The in-flight entry was released by the goroutine's cleanup: a second
	// ScheduleRefresh for the same barcode is accepted and reaches the
	// upstream rather than being dropped as a duplicate.
	off.reopen()
	off.unblock()
	refresher.ScheduleRefresh(context.Background(), productID)
	refresher.Wait()

	if got := off.CallCount(productID); got != 2 {
		t.Fatalf("second ScheduleRefresh after Wait: want upstream call count 2, got %d", got)
	}
}

type ctxBoundUpstream struct {
	mu        sync.Mutex
	result    ProductSummary
	started   chan struct{}
	release   chan struct{}
	once      sync.Once
	lookupErr error
}

func newCtxBoundUpstream(result ProductSummary) *ctxBoundUpstream {
	return &ctxBoundUpstream{
		result:  result,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (u *ctxBoundUpstream) LookupIn(ctx context.Context, source ExternalSource, barcode string) (*ProductSummary, error) {
	u.once.Do(func() { close(u.started) })
	select {
	case <-u.release:
		if err := ctx.Err(); err != nil {
			u.record(err)
			return nil, err
		}
		out := u.result
		out.ID = barcode
		return &out, nil
	case <-ctx.Done():
		err := ctx.Err()
		u.record(err)
		return nil, err
	}
}

func (u *ctxBoundUpstream) record(err error) {
	u.mu.Lock()
	u.lookupErr = err
	u.mu.Unlock()
}

func (u *ctxBoundUpstream) err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lookupErr
}

func waitForRefresh(t *testing.T, refresher *Refresher) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		refresher.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not finish")
	}
}

func TestRefresh_SavesWhenLookupContextIsCanceled(t *testing.T) {
	const productID = "012345678905"

	catalog := newRefreshCatalog(t)
	if err := catalog.CreateProduct(context.Background(), Product{
		ID:     productID,
		Name:   "Old Name",
		Source: SourceExternal,
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	refresher := &Refresher{
		Catalog:               catalog,
		Upstream:              &cancelingUpstream{cancel: cancel, result: ProductSummary{Name: "New Name"}},
		ExternalLookupEnabled: true,
		BackgroundTimeout:     time.Second,
	}

	outcome, err := refresher.Refresh(ctx, productID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != OutcomeUpdated {
		t.Fatalf("outcome: want %q, got %q", OutcomeUpdated, outcome)
	}

	got, err := catalog.GetProductByID(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("product missing after refresh")
	}
	if got.Name != "New Name" {
		t.Fatalf("name: want %q, got %q", "New Name", got.Name)
	}
}

type cancelingUpstream struct {
	cancel context.CancelFunc
	result ProductSummary
}

func (u *cancelingUpstream) LookupIn(ctx context.Context, source ExternalSource, barcode string) (*ProductSummary, error) {
	u.cancel()
	out := u.result
	out.ID = barcode
	return &out, nil
}

func TestScheduleRefresh_CallerCancelDoesNotAbortLookup(t *testing.T) {
	const productID = "012345678905"

	catalog := newRefreshCatalog(t)
	if err := catalog.CreateProduct(context.Background(), Product{
		ID:     productID,
		Name:   "Old Name",
		Source: SourceExternal,
	}); err != nil {
		t.Fatal(err)
	}
	upstream := newCtxBoundUpstream(ProductSummary{Name: "New Name"})
	refresher := &Refresher{
		Catalog:               catalog,
		Upstream:              upstream,
		TTL:                   time.Hour,
		ExternalLookupEnabled: true,
		BackgroundTimeout:     time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	refresher.ScheduleRefresh(ctx, productID)

	select {
	case <-upstream.started:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup did not start")
	}
	cancel()
	close(upstream.release)
	waitForRefresh(t, refresher)

	got, err := catalog.GetProductByID(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("product missing after refresh")
	}
	if got.Name != "New Name" {
		t.Fatalf("name after caller cancel: want %q, got %q", "New Name", got.Name)
	}
	if upstream.err() != nil {
		t.Fatalf("lookup error after caller cancel: %v", upstream.err())
	}
}

func TestScheduleRefresh_BackgroundTimeoutStampsRefresh(t *testing.T) {
	const productID = "012345678905"

	catalog := newRefreshCatalog(t)
	if err := catalog.CreateProduct(context.Background(), Product{
		ID:     productID,
		Name:   "Old Name",
		Source: SourceExternal,
	}); err != nil {
		t.Fatal(err)
	}
	upstream := newCtxBoundUpstream(ProductSummary{Name: "New Name"})
	refresher := &Refresher{
		Catalog:               catalog,
		Upstream:              upstream,
		TTL:                   time.Hour,
		ExternalLookupEnabled: true,
		BackgroundTimeout:     30 * time.Millisecond,
	}

	baseline := runtime.NumGoroutine()
	refresher.ScheduleRefresh(context.Background(), productID)
	waitForRefresh(t, refresher)

	if err := upstream.err(); err != context.DeadlineExceeded {
		t.Fatalf("lookup error: want %v, got %v", context.DeadlineExceeded, err)
	}
	got, err := catalog.GetProductByID(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("product missing after refresh")
	}
	if got.Name != "Old Name" {
		t.Fatalf("name after timeout: want %q, got %q", "Old Name", got.Name)
	}
	if got.RefreshedAt == nil {
		t.Fatal("refreshed_at was not stamped after the upstream timeout")
	}
	assertNoLeakedGoroutines(t, baseline)
}
