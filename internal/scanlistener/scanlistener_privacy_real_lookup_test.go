package scanlistener

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

type countingUpstream struct{ calls atomic.Int32 }

func (u *countingUpstream) Lookup(ctx context.Context, barcode string) product.FanOutResult {
	u.calls.Add(1)
	return product.FanOutResult{
		Outcome: product.FanOutHit,
		Product: &product.ProductSummary{ID: "upstream-would-hit", Name: "Upstream", Category: "Food"},
		Source:  product.ExternalSourceOpenFoodFacts,
	}
}

// USB scanner path wired to the REAL LookupService: QR and local-only payloads
// must never reach the upstream (fixture: upstream would answer with a hit) and
// must never write barcode_misses; a product barcode still reaches upstream.
func TestScanListener_PrivacyWithRealLookupService(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantStatus   scan.ScanStatus
		wantUpstream int32
	}{
		{"wifi", "WIFI:S:home;P:secret;;\n", scan.QR, 0},
		{"vcard", "BEGIN:VCARD;FN:Test;END:VCARD\n", scan.QR, 0},
		{"15 digits", "123456789012345\n", scan.QR, 0},
		{"21-char token", strings.Repeat("A", 19) + "12\n", scan.QR, 0},
		{"letters only", "ABCDEFGH\n", scan.QR, 0},
		{"https url", "https://example.com/x\n", scan.QR, 0},
		{"local-only FNSKU (local miss)", "X002MB5N7L\n", scan.Flagged, 0},
		{"local-only lowercase", "x002mb5n7l\n", scan.Flagged, 0},
		{"product 12 digits (control: upstream is reachable)", "036000291452\n", scan.Pending, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := app.OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := app.RunMigrations(db); err != nil {
				t.Fatal(err)
			}
			scope, err := auth.PrimaryScope(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			up := &countingUpstream{}
			l := newListener(t)
			l.Scope = scope
			l.Source = SourceStdin
			l.Queue = &fakeQueue{}
			l.LookupService = &product.LookupService{Catalog: product.NewCatalog(db), Upstream: up}
			l.Stdin = strings.NewReader(tc.line)

			l.Run(context.Background())

			entries := l.Queue.(*fakeQueue).entries
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}
			if entries[0].Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", entries[0].Status, tc.wantStatus)
			}
			if got := up.calls.Load(); got != tc.wantUpstream {
				t.Fatalf("upstream calls = %d, want %d", got, tc.wantUpstream)
			}
			var misses int
			if err := db.QueryRow(`SELECT COUNT(*) FROM barcode_misses`).Scan(&misses); err != nil {
				t.Fatal(err)
			}
			if misses != 0 {
				t.Fatalf("barcode_misses rows = %d, want 0", misses)
			}
		})
	}
}
