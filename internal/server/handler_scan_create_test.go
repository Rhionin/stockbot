package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
)

// TestScanCreateHandlerLookupOutcomes locks in the found/not-found behavior of
// ScanCreateHandler.Handle after its task 6.3 refactor to call
// scan.NewEntryFromLookup: a found product still yields status "pending" with
// the matched productId, and a lookup miss still yields status "flagged" with
// a nil productId.
func TestScanCreateHandlerLookupOutcomes(t *testing.T) {
	tests := []handlerTestCase{
		{
			name:  "found product yields pending status with productId",
			setup: setupProductWithBarcode("prod-scan-found", "Found Product", "Test", "111222333444"),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"111222333444","userId":"user-scan-found"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-scan-found"},
				},
			},
		},
		{
			name: "no matching product yields flagged status with nil productId",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"000111222333","userId":"user-scan-notfound"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestScanCreateHandler_ControlBarcodeUsesModeEndpoint documents how the
// browser control-barcode gap is now closed. ScanCreateHandler itself still
// treats any barcode it is given as a product scan; the fix is that the
// browser no longer POSTs a recognized control barcode to /api/scans. Instead
// it reads the reserved strings from GET /api/scanner/config and, on a match,
// calls POST /api/scanner/mode, which switches the shared mode and broadcasts a
// scanner_mode event rather than enqueuing a flagged product scan. This test
// pins that supported flow: the config endpoint reports STOCK_IN, and posting
// it to the mode endpoint switches the mode instead of creating a scan entry.
func TestScanCreateHandler_ControlBarcodeUsesModeEndpoint(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "STOCK_IN control barcode switches mode via /api/scanner/mode and creates no scan entry",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/scanner/config",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.stockInBarcode", value: "STOCK_IN"},
					{path: "$.stockOutBarcode", value: "STOCK_OUT"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/scanner/mode",
					body:           `{"mode":"stock_in"}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.mode", value: "stock_in"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-scan-control"},
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"[]"},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}

// TestScanCreateHandler_DefaultModeIngestContract pins the backend half of the
// "scans not appearing" fix: a scan taken in the default scanner mode
// (stock_in) is persisted and returned by GET /api/scans with its direction
// intact. A missed lookup here yields flagged; a found product yields pending.
// Either way the entry is retrievable, so the backend ingest path is never the
// reason a default-mode scan fails to show up in the UI.
func TestScanCreateHandler_DefaultModeIngestContract(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "flagged default-mode scan is returned by GET /api/scans with direction stock_in",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"901234567890","userId":"user-default-mode","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.direction", value: "stock_in"},
					{path: "$.status", value: "flagged"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-default-mode"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].direction", value: "stock_in"},
						{path: "$[0].status", value: "flagged"},
						{path: "$[0].barcode", value: "901234567890"},
					},
				},
			),
		},
		{
			name:  "pending default-mode scan for a known product is returned with direction stock_in",
			setup: setupProductWithBarcode("prod-default-mode", "Default Mode Product", "Test", "808080808080"),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"808080808080","userId":"user-default-mode-found","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.direction", value: "stock_in"},
					{path: "$.status", value: "pending"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-default-mode-found", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].direction", value: "stock_in"},
						{path: "$[0].status", value: "pending"},
						{path: "$[0].productId", value: "prod-default-mode"},
					},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}

// TestScanCreateHandler_MergeBehavior locks in that a repeat POST /api/scans
// for the same barcode/userId/direction while the first entry is still
// pending or flagged merges into the existing entry (unitCount incremented)
// instead of creating a duplicate row, through the full HTTP path.
func TestScanCreateHandler_MergeBehavior(t *testing.T) {
	tests := []handlerTestCase{
		{
			name:  "repeat scan while pending merges into existing entry with unitCount 2",
			setup: setupProductWithBarcode("prod-scan-merge-pending", "Merge Pending Product", "Test", "222333444555"),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"222333444555","userId":"user-scan-merge-pending","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/scans",
					body:           `{"barcode":"222333444555","userId":"user-scan-merge-pending","direction":"stock_in"}`,
					expectedStatus: http.StatusCreated,
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-scan-merge-pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].status", value: "pending"},
						{path: "$[0].unitCount", value: float64(2)},
					},
				},
			),
		},
		{
			name: "repeat scan while flagged merges into existing entry with unitCount 2",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"333444555666","userId":"user-scan-merge-flagged","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/scans",
					body:           `{"barcode":"333444555666","userId":"user-scan-merge-flagged","direction":"stock_in"}`,
					expectedStatus: http.StatusCreated,
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-scan-merge-flagged"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].status", value: "flagged"},
						{path: "$[0].unitCount", value: float64(2)},
					},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}

// TestScanCreateHandler_RescanSortsWithNewestScans is the HTTP contract for a
// repeat scan of an item already on the queue. GET /api/scans lists oldest
// scanned_at first; the UI sorts that same column newest-first. After oats,
// milk, then oats again, the rescan merges into the oats row, increments the
// count once, and leaves milk at one. Oats is last in the list (first on
// screen) and its scannedAt is later than milk's, which is what a reload and
// every SSE client order by.
func TestScanCreateHandler_RescanSortsWithNewestScans(t *testing.T) {
	const userID = "user-rescan-order"
	tests := []handlerTestCase{
		{
			name: "rescanning an open item moves it to the newest scanned_at",
			setup: func(env testEnv) {
				setupProductWithBarcode("prod-rescan-oats", "Rescan Oats", "Grocery", "810000000001")(env)
				setupProductWithBarcode("prod-rescan-milk", "Rescan Milk", "Grocery", "810000000002")(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"810000000001","userId":"user-rescan-order","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				var first scanRowJSON
				if err := json.NewDecoder(env.Res.Body).Decode(&first); err != nil {
					env.T.Fatalf("decode first scan: %v", err)
				}

				milkBody := exchangeJSON(env.T, handler, http.MethodPost, "/api/scans",
					`{"barcode":"810000000002","userId":"user-rescan-order","direction":"stock_in"}`, http.StatusCreated)
				var milk scanRowJSON
				if err := json.Unmarshal(milkBody, &milk); err != nil {
					env.T.Fatalf("decode milk scan: %v", err)
				}

				oatsBody := exchangeJSON(env.T, handler, http.MethodPost, "/api/scans",
					`{"barcode":"810000000001","userId":"user-rescan-order","direction":"stock_in"}`, http.StatusCreated)
				var oats scanRowJSON
				if err := json.Unmarshal(oatsBody, &oats); err != nil {
					env.T.Fatalf("decode oats rescan: %v", err)
				}
				if oats.ID != first.ID {
					env.T.Fatalf("rescan id: want merged %s, got %s", first.ID, oats.ID)
				}
				if oats.Barcode != "810000000001" {
					env.T.Fatalf("rescan barcode: want 810000000001, got %s", oats.Barcode)
				}
				if oats.UnitCount != 2 {
					env.T.Fatalf("rescan unitCount: want 2, got %d", oats.UnitCount)
				}
				if !oats.ScannedAt.After(milk.ScannedAt) || !oats.ScannedAt.After(first.ScannedAt) {
					env.T.Fatalf("rescan scannedAt %s should be after milk %s and first oats %s", oats.ScannedAt, milk.ScannedAt, first.ScannedAt)
				}

				listBody := exchangeJSON(env.T, handler, http.MethodGet, "/api/scans?userId="+userID+"&status=pending", "", http.StatusOK)
				var listed []scanRowJSON
				if err := json.Unmarshal(listBody, &listed); err != nil {
					env.T.Fatalf("decode scan list: %v", err)
				}
				if len(listed) != 2 {
					env.T.Fatalf("want 2 pending scans, got %d", len(listed))
				}
				if listed[0].Barcode != "810000000002" || listed[1].Barcode != "810000000001" {
					env.T.Fatalf("oldest-first list: want milk then rescanned oats, got %s then %s", listed[0].Barcode, listed[1].Barcode)
				}
				if listed[0].UnitCount != 1 {
					env.T.Fatalf("milk unitCount: want 1, got %d", listed[0].UnitCount)
				}
				if listed[1].ID != oats.ID || listed[1].UnitCount != 2 || !listed[1].ScannedAt.Equal(oats.ScannedAt) {
					env.T.Fatalf("reloaded oats: want id %s unitCount 2 scannedAt %s, got %+v", oats.ID, oats.ScannedAt, listed[1])
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

type scanRowJSON struct {
	ID        string    `json:"id"`
	Barcode   string    `json:"barcode"`
	ScannedAt time.Time `json:"scannedAt"`
	UnitCount int       `json:"unitCount"`
}

func TestScanCreateHandler_QRPayloadSkipsLookup(t *testing.T) {
	const pageURL = "https://example.com/groceries/milk"
	tests := []handlerTestCase{
		{
			name:  "a URL that matches a stored barcode is still a QR row",
			setup: setupProductWithBarcode("prod-qr-url", "Should Not Match", "Test", pageURL),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"https://example.com/groceries/milk","userId":"user-qr-url","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
					{path: "$.productId", value: nil},
					{path: "$.barcode", value: pageURL},
					{path: "$.direction", value: "stock_in"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-qr-url", "status": "qr"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].status", value: "qr"},
						{path: "$[0].barcode", value: pageURL},
						{path: "$[0].productId", value: nil},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-qr-url", "status": "pending"},
					expectedStatus: http.StatusOK,
					bodyExcludes:   []string{pageURL, `"status":"qr"`},
				},
			),
		},
		{
			name: "Amazon FNSKU is looked up, not stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"X002MB5N7L","userId":"user-fnsku","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
					{path: "$.barcode", value: "X002MB5N7L"},
				},
			},
		},
		{
			name: "Amazon ASIN is looked up, not stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"B08XYZ1234","userId":"user-asin","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
				},
			},
		},
		{
			name: "a bare text token is looked up, not stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"hello","userId":"user-bare-text","direction":"stock_out"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
				},
			},
		},
		{
			name: "buy milk with a space is stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"buy milk","userId":"user-buy-milk","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
					{path: "$.productId", value: nil},
				},
			},
		},
		{
			name: "a hyphenated digit token is looked up",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"1234-5678","userId":"user-hyphen-token","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
				},
			},
		},
		{
			name: "a 15-digit string is looked up, not stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"123456789012345","userId":"user-15-digit","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
				},
			},
		},
		{
			name: "a 33-character token is stored as QR",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + strings.Repeat("C", 33) + `","userId":"user-33","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
				},
			},
		},
		{
			name: "an unknown EAN-13 is still looked up and flagged",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"4006381333931","userId":"user-ean-13","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
					{path: "$.productId", value: nil},
				},
			},
		},
		{
			name: "an unknown ITF-14 is still looked up and flagged",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"10012345678902","userId":"user-itf-14","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
				},
			},
		},
		{
			name:  "a known UPC-A is still a pending product scan",
			setup: setupProductWithBarcode("prod-upc-a", "Cola", "Drinks", "036000291452"),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"036000291452","userId":"user-upc-a","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-upc-a"},
				},
			},
		},
		{
			name: "an unknown EAN-8 is still looked up and flagged",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"96385074","userId":"user-ean-8","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

func upstreamTotalCalls(u *fakeUpstream) int {
	if u == nil {
		return 0
	}
	total := 0
	for _, fake := range u.databases {
		total += fake.TotalCalls()
	}
	return total
}

func TestScanCreateHandler_QRPayloadPrivacy(t *testing.T) {
	wifiSecret := strings.Repeat("s", 80)
	wifiPayload := "WIFI:T:WPA;S:MyNet;P:" + strings.Repeat("x", 120) + wifiSecret + ";;"
	const mailto = "mailto:you@example.com"
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	tests := []handlerTestCase{
		{
			name: "WIFI QR skips lookup and redacts logs",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + wifiPayload + `","userId":"user-wifi","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
					{path: "$.productId", value: nil},
				},
				bodyExcludes: []string{wifiSecret},
			},
			afterRequest: func(env testEnv) {
				if upstreamTotalCalls(env.Upstream) != 0 {
					env.T.Fatalf("upstream lookups = %d, want 0", upstreamTotalCalls(env.Upstream))
				}
				logText := logs.String()
				if strings.Contains(logText, wifiSecret) || strings.Contains(logText, wifiPayload) {
					env.T.Fatalf("logs leaked wifi payload: %q", logText)
				}
				if !strings.Contains(logText, "qr payload") {
					env.T.Fatalf("logs = %q", logText)
				}
			},
		},
		{
			name: "mailto QR skips lookup",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + mailto + `","userId":"user-mail","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
				},
			},
			afterRequest: func(env testEnv) {
				if upstreamTotalCalls(env.Upstream) != 0 {
					env.T.Fatalf("upstream lookups = %d, want 0", upstreamTotalCalls(env.Upstream))
				}
			},
		},
		{
			name: "FNSKU still runs lookup",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"X002MB5N7L","userId":"user-fnsku-privacy","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "flagged"},
				},
			},
			afterRequest: func(env testEnv) {
				if env.Upstream.Database(product.ExternalSourceOpenFoodFacts).CallCount("X002MB5N7L") == 0 {
					env.T.Fatal("expected external lookup for FNSKU")
				}
			},
		},
	}

	runHandlerTests(t, tests)
}
