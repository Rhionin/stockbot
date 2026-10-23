package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/Rhionin/stockbot/internal/product"
)

// fixtureHousehold is an invented household. Names, ids, markers, and
// barcodes are synthetic. This suite must never copy a real household.
type fixtureHousehold struct {
	ID        string
	Name      string
	Marker    string
	Barcode   string
	ProductID string
}

// leakMarkers are the strings a response must not contain when this
// household's data should be invisible. They are the customer-visible
// sentinels, checked on the HTTP body so the same assertion can run
// against Postgres or DSQL later without reading SQLite-specific rows.
func (h fixtureHousehold) leakMarkers() []string {
	return []string{h.ID, h.Name, h.Marker, h.Barcode, h.ProductID}
}

// principal stamps a request so the server treats it as one household.
// Callers use Apply. The concrete type is what S4 replaces.
type principal interface {
	Household() fixtureHousehold
	Apply(r *http.Request)
}

// user1Principal acts as today's only account, user-1, on behalf of a
// fixture household. The server has no household selector, so Apply does
// not send one. S4 replaces this method with a session or scope for
// Household(); call sites stay on Apply.
type user1Principal struct {
	household fixtureHousehold
}

func (p user1Principal) Household() fixtureHousehold { return p.household }

func (p user1Principal) Apply(*http.Request) {
	// The process has one account, user-1, and the server does not read a
	// household selector. Apply is the seam #377 replaces. It deliberately
	// leaves the request unchanged: a header the server ignores would look
	// like authentication.
}

// twoHouseholds is one test's Alder and Birch, each seeded through the
// product catalog so a handler that echoed stored names would fail.
type twoHouseholds struct {
	T       *testing.T
	Handler http.Handler
	Alder   fixtureHousehold
	Birch   fixtureHousehold
}

func (fx *twoHouseholds) principalFor(h fixtureHousehold) principal {
	return user1Principal{household: h}
}

// newTwoHouseholds builds the two invented households on a migrated
// database and the same handler the server tests use.
func newTwoHouseholds(t *testing.T) *twoHouseholds {
	t.Helper()
	alder := fixtureHousehold{
		ID:        "01901111-1111-7000-8000-0000000000a1",
		Name:      "Household Alder",
		Marker:    "alder-sentinel-k7m2q",
		Barcode:   "2001000000012",
		ProductID: "01901111-1111-7000-8000-000000000aa1",
	}
	birch := fixtureHousehold{
		ID:        "01902222-2222-7000-8000-0000000000b2",
		Name:      "Household Birch",
		Marker:    "birch-sentinel-q9p4w",
		Barcode:   "2002000000026",
		ProductID: "01902222-2222-7000-8000-000000000bb2",
	}
	if err := validateInventedHouseholds(alder, birch); err != nil {
		t.Fatalf("household fixtures: %v", err)
	}

	handler, env := setupTestWithDB(t)
	seedHousehold(t, env, alder)
	seedHousehold(t, env, birch)
	return &twoHouseholds{T: t, Handler: handler, Alder: alder, Birch: birch}
}

func seedHousehold(t *testing.T, env testEnv, h fixtureHousehold) {
	t.Helper()
	ctx := context.Background()
	// The product name carries the marker and the household id, and the
	// category carries the household name. Any of those strings in a
	// response is this household's data. The id has to be stored or it
	// cannot leak, and then it is not a marker.
	err := env.ProductStore.CreateProduct(ctx, product.Product{
		ID:       h.ProductID,
		Name:     h.Marker + " " + h.ID,
		Category: h.Name,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", h.Name, err)
	}
	if err := env.ProductStore.UpsertBarcodeMapping(ctx, h.Barcode, h.ProductID, "global", ""); err != nil {
		t.Fatalf("seed %s barcode: %v", h.Name, err)
	}
	got, err := env.ProductStore.GetProductByID(ctx, h.ProductID)
	if err != nil || got == nil || got.Name != h.Marker+" "+h.ID || got.Category != h.Name {
		t.Fatalf("seed %s did not store the marker (product %+v, err %v)", h.Name, got, err)
	}
	summary, err := env.ProductStore.LookupByBarcode(ctx, h.Barcode, "")
	if err != nil || summary == nil || summary.Name != h.Marker+" "+h.ID {
		t.Fatalf("seed %s barcode was not stored (summary %+v, err %v)", h.Name, summary, err)
	}
}

// leakFatal is the part of testing.TB that assertNoLeak uses. A fake
// records Fatalf so a test can prove the check is able to fail.
type leakFatal interface {
	Helper()
	Fatalf(format string, args ...any)
}

// assertNoLeak fails when body contains any sentinel from other.
// Pass the household that must not be visible. The check is on the
// response bytes, not on a SQL dialect.
func assertNoLeak(t leakFatal, body []byte, other fixtureHousehold) {
	t.Helper()
	for _, marker := range other.leakMarkers() {
		if marker == "" {
			t.Fatalf("%s has an empty leak marker", other.Name)
		}
		if bytes.Contains(body, []byte(marker)) {
			t.Fatalf("response contains %s marker %q", other.Name, marker)
		}
	}
}

// leakReport records the first Fatalf from assertNoLeak.
type leakReport struct {
	failed bool
	msg    string
}

func (r *leakReport) Helper() {}

func (r *leakReport) Fatalf(format string, args ...any) {
	if r.failed {
		return
	}
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

func TestAssertNoLeakRejectsOtherHousehold(t *testing.T) {
	fx := newTwoHouseholds(t)
	// Synthetic body, requested as Alder. This does not read GET /api/products:
	// that route is quarantined until #377, and asserting today's leak would
	// fail once products are scoped to one household.
	body := []byte("requested as " + fx.Alder.Name + " leaked " + fx.Birch.Marker)
	report := &leakReport{}
	assertNoLeak(report, body, fx.Birch)
	if !report.failed || !strings.Contains(report.msg, fx.Birch.Name) || !strings.Contains(report.msg, fx.Birch.Marker) {
		t.Fatalf("assertNoLeak(Birch marker, requested as Alder) = failed:%v msg:%q", report.failed, report.msg)
	}
}

func TestAssertNoLeakRejectsHouseholdID(t *testing.T) {
	fx := newTwoHouseholds(t)
	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(fx.Alder.ID)) {
		t.Fatalf("GET /api/products as %s did not contain household id %s (status %d, body %s)", fx.Alder.Name, fx.Alder.ID, rec.Code, snippet(rec.Body.Bytes()))
	}

	report := &leakReport{}
	assertNoLeak(report, []byte(fx.Alder.ID), fx.Alder)
	if !report.failed || !strings.Contains(report.msg, fx.Alder.ID) {
		t.Fatalf("assertNoLeak(Alder household id) = failed:%v msg:%q", report.failed, report.msg)
	}
}

func validateInventedHouseholds(households ...fixtureHousehold) error {
	seen := map[string]string{}
	for _, h := range households {
		if h.Name == "" || h.Marker == "" {
			return errFixture("a household is missing its name or marker")
		}
		if !uuidV7Shape(h.ID) || !uuidV7Shape(h.ProductID) {
			return errFixture(h.Name + " ids must be UUIDv7-shaped and invented")
		}
		if h.ID == h.ProductID {
			return errFixture(h.Name + " reuses one id for the household and the product")
		}
		if !inStoreBarcode(h.Barcode) {
			return errFixture(h.Name + " barcode must be a synthetic 200 in-store code")
		}
		for _, marker := range h.leakMarkers() {
			if prev, ok := seen[marker]; ok {
				return errFixture("marker " + marker + " is shared by " + prev + " and " + h.Name)
			}
			seen[marker] = h.Name
		}
	}
	return nil
}

type fixtureError string

func (e fixtureError) Error() string { return string(e) }

func errFixture(msg string) error { return fixtureError(msg) }

func uuidV7Shape(id string) bool {
	// 8-4-4-4-12 hex, version nibble 7, RFC variant nibble 8, 9, a, or b.
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '7' {
				return false
			}
		case 19:
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				return false
			}
		default:
			if !unicode.Is(unicode.ASCII_Hex_Digit, c) {
				return false
			}
		}
	}
	return true
}

func inStoreBarcode(s string) bool {
	if len(s) != 13 || len(s) < 3 || s[:3] != "200" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
