package server

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
)

func seedFixtureHit(env testEnv, productID, barcode string) {
	setupProductWithBarcode(productID, "Fixture Hit", "Test", barcode)(env)
}

func assertNoLookupOrMisses(env testEnv) {
	env.T.Helper()
	if n := upstreamTotalCalls(env.Upstream); n != 0 {
		env.T.Fatalf("upstream lookups = %d, want 0", n)
	}
	var missCount int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM barcode_misses`).Scan(&missCount); err != nil {
		env.T.Fatalf("count barcode_misses: %v", err)
	}
	if missCount != 0 {
		env.T.Fatalf("barcode_misses rows = %d, want 0", missCount)
	}
}

func TestScanCreateHandler_LookupEligibilityPMRule(t *testing.T) {
	const (
		wifiPayload = "WIFI:S:home;P:secret;;"
		vCard       = "BEGIN:VCARD\nFN:Test User\nEND:VCARD"
	)
	token32 := strings.Repeat("A", 32)
	token33 := strings.Repeat("B", 33)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	tests := []handlerTestCase{
		{
			name: "Wi-Fi QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-wifi-fixture", wifiPayload)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"WIFI:S:home;P:secret;;","userId":"user-wifi-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
					{path: "$.productId", value: nil},
				},
			},
			afterRequest: func(env testEnv) {
				assertNoLookupOrMisses(env)
				if strings.Contains(logs.String(), wifiPayload) || strings.Contains(logs.String(), "secret") {
					env.T.Fatalf("logs leaked qr payload: %q", logs.String())
				}
			},
		},
		{
			name: "vCard QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-vcard-fixture", vCard)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"BEGIN:VCARD\nFN:Test User\nEND:VCARD","userId":"user-vcard-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "qr"},
					{path: "$.productId", value: nil},
				},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "FNSKU lookup with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-fnsku-pm", "X002MB5N7L")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"X002MB5N7L","userId":"user-fnsku-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-fnsku-pm"},
				},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "15-digit QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-15-digit", "123456789012345")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"123456789012345","userId":"user-15-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "20-digit QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-20-digit", "12345678901234567890")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"12345678901234567890","userId":"user-20-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "padded https URL QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-url-pad", "https://example.com/x")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"  https://example.com/x \r\n","userId":"user-url-pad","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "uppercase HTTP URL QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-http-up", "HTTP://EXAMPLE.COM/path")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"HTTP://EXAMPLE.COM/path","userId":"user-http-up","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "www URL QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-www", "www.example.com/item")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"www.example.com/item","userId":"user-www","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "20-char local token lookup with fixture hit",
			setup: func(env testEnv) {
				token20 := strings.Repeat("A", 18) + "12"
				seedFixtureHit(env, "prod-token-20", token20)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + strings.Repeat("A", 18) + "12" + `","userId":"user-20","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-token-20"},
				},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "32-char letters-only QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-token-32", token32)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + token32 + `","userId":"user-32","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "letters-only 8-char QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-abcdefgh", "ABCDEFGH")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"ABCDEFGH","userId":"user-abc8","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "33-char token QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-token-33", token33)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"` + token33 + `","userId":"user-33-pm","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
		{
			name: "4-char token lookup with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-ab12", "AB12")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"AB12","userId":"user-ab12","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-ab12"},
				},
			},
		},
		{
			name: "3-char token QR with fixture hit",
			setup: func(env testEnv) {
				seedFixtureHit(env, "prod-ab1", "AB1")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"AB1","userId":"user-ab1","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions:     []assertion{{path: "$.status", value: "qr"}},
			},
			afterRequest: func(env testEnv) { assertNoLookupOrMisses(env) },
		},
	}

	runHandlerTests(t, tests)
}