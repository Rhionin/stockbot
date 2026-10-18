package product

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinrixx/retryhttp"
)

// mockTransport wraps a function as an http.RoundTripper for testing.
type mockTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestLookupBarcode(t *testing.T) {
	tests := []struct {
		name         string
		barcode      string
		statusCode   int
		responseBody string
		wantErr      error
		checkProduct func(*testing.T, *ProductSummary)
	}{
		{
			name:       "success",
			barcode:    "012345678905",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "Coca-Cola",
					"categories": "Beverages",
					"code": "012345678905",
					"image_front_small_url": "https://images.openfoodfacts.org/images/products/012/345/678/905/front_en.200.jpg"
				}
			}`,
			checkProduct: func(t *testing.T, ps *ProductSummary) {
				if ps == nil {
					t.Fatal("expected ProductSummary, got nil")
				}
				if ps.Name != "Coca-Cola" {
					t.Errorf("expected name 'Coca-Cola', got %q", ps.Name)
				}
				if ps.Category != "Beverages" {
					t.Errorf("expected category 'Beverages', got %q", ps.Category)
				}
				if ps.ID != "012345678905" {
					t.Errorf("expected ID '012345678905', got %q", ps.ID)
				}
				if want := "https://images.openfoodfacts.org/images/products/012/345/678/905/front_en.200.jpg"; ps.ImageURL != want {
					t.Errorf("expected ImageURL %q, got %q", want, ps.ImageURL)
				}
			},
		},
		{
			name:       "success without image",
			barcode:    "012345678906",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "Generic Snack",
					"categories": "Snacks",
					"code": "012345678906"
				}
			}`,
			checkProduct: func(t *testing.T, ps *ProductSummary) {
				if ps == nil {
					t.Fatal("expected ProductSummary, got nil")
				}
				if ps.ImageURL != "" {
					t.Errorf("expected empty ImageURL, got %q", ps.ImageURL)
				}
			},
		},
		{
			name:       "not found - 404",
			barcode:    "999999999999",
			statusCode: http.StatusNotFound,
			wantErr:    ErrProductNotFound,
		},
		{
			name:       "not found - invalid status",
			barcode:    "012345678905",
			statusCode: 200,
			responseBody: `{
				"status": 0,
				"product": {}
			}`,
			wantErr: ErrProductNotFound,
		},
		{
			name:       "not found - empty name",
			barcode:    "012345678905",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "",
					"categories": "Test"
				}
			}`,
			wantErr: ErrProductNotFound,
		},
		{
			name:    "empty barcode",
			barcode: "",
			wantErr: errors.New("barcode cannot be empty"),
		},
		{
			name:         "malformed JSON",
			barcode:      "012345678905",
			statusCode:   200,
			responseBody: "invalid json",
			wantErr:      errors.New("failed to decode"),
		},
		{
			name:       "server error",
			barcode:    "012345678905",
			statusCode: http.StatusInternalServerError,
			wantErr:    errors.New("Product Opener database returned status 500"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip HTTP setup for validation-only tests
			if tt.barcode == "" {
				client := NewProductOpenerClient(ExternalSourceOpenFoodFacts, "https://world.openfoodfacts.org/api/v2/product")
				_, err := client.LookupBarcode(context.Background(), tt.barcode)
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}

			mock := mockTransport{
				fn: func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(bytes.NewReader([]byte(tt.responseBody))),
						Header:     make(http.Header),
					}, nil
				},
			}

			client := NewProductOpenerClientWithHTTPClient(ExternalSourceOpenFoodFacts, "https://world.openfoodfacts.org/api/v2/product", &http.Client{
				Timeout:   10 * time.Second,
				Transport: mock,
			})

			ps, err := client.LookupBarcode(context.Background(), tt.barcode)

			// Check error
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) && !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if ps != nil {
					t.Errorf("expected nil ProductSummary on error, got %v", ps)
				}
				return
			}

			// Check success
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.checkProduct != nil {
				tt.checkProduct(t, ps)
			}
		})
	}
}

func TestShouldRetryIncluding429(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		method     string
		want       bool
	}{
		{
			name:       "retry on 429",
			statusCode: http.StatusTooManyRequests,
			method:     http.MethodGet,
			want:       true,
		},
		{
			name:       "retry on 503 for GET (delegates to default)",
			statusCode: http.StatusServiceUnavailable,
			method:     http.MethodGet,
			want:       true,
		},
		{
			name:       "do not retry on 404",
			statusCode: http.StatusNotFound,
			method:     http.MethodGet,
			want:       false,
		},
		{
			name:       "do not retry on 400",
			statusCode: http.StatusBadRequest,
			method:     http.MethodGet,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempt := retryhttp.Attempt{
				Req: &http.Request{
					Method: tt.method,
				},
				Res: &http.Response{
					StatusCode: tt.statusCode,
				},
			}

			got := shouldRetryIncluding429(attempt)
			if got != tt.want {
				t.Errorf("shouldRetryIncluding429() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultProductOpenerClients(t *testing.T) {
	// **Validates: Requirements 7.2, 7.3**
	// Assert all four clients carry the identical 10s timeout and the same retry
	// transport configuration, and that each BaseURL() matches its
	// productOpenerBaseURLs entry.

	clients := DefaultProductOpenerClients()

	if len(clients) != 4 {
		t.Errorf("expected 4 clients, got %d", len(clients))
	}

	// Check each client is properly configured
	expectedSources := []ExternalSource{
		ExternalSourceOpenFoodFacts,
		ExternalSourceOpenProductsFacts,
		ExternalSourceOpenBeautyFacts,
		ExternalSourceOpenPetFoodFacts,
	}

	expectedURLs := map[ExternalSource]string{
		ExternalSourceOpenFoodFacts:     "https://world.openfoodfacts.org/api/v2/product",
		ExternalSourceOpenProductsFacts: "https://world.openproductsfacts.org/api/v2/product",
		ExternalSourceOpenBeautyFacts:   "https://world.openbeautyfacts.org/api/v2/product",
		ExternalSourceOpenPetFoodFacts:  "https://world.openpetfoodfacts.org/api/v2/product",
	}

	expectedTimeout := 10 * time.Second
	var firstTransportType string

	for _, source := range expectedSources {
		client, ok := clients[source]
		if !ok {
			t.Errorf("expected client for source %q", source)
			continue
		}

		// Cast to *ProductOpenerClient to access Source(), BaseURL(), and httpClient
		poclient := client.(*ProductOpenerClient)

		// Requirement 7.2: Source accessor works correctly
		if poclient.Source() != source {
			t.Errorf("client.Source(): want %q, got %q", source, poclient.Source())
		}

		// Requirement 7.3: BaseURL() matches the productOpenerBaseURLs entry
		expectedURL := expectedURLs[source]
		if poclient.BaseURL() != expectedURL {
			t.Errorf("client.BaseURL() for %q: want %q, got %q", source, expectedURL, poclient.BaseURL())
		}

		// Requirement 7.3: All four clients carry the identical 10s timeout
		if poclient.httpClient.Timeout != expectedTimeout {
			t.Errorf("client for %q: Timeout = %v, want %v", source, poclient.httpClient.Timeout, expectedTimeout)
		}

		// Requirement 7.2: All four clients have the same retry transport configuration
		transportType := fmt.Sprintf("%T", poclient.httpClient.Transport)
		if firstTransportType == "" {
			firstTransportType = transportType
		}
		if transportType != firstTransportType {
			t.Errorf("client for %q: Transport type = %s, want %s (same as all others)", source, transportType, firstTransportType)
		}

		// Verify the transport is a retryhttp transport (not a default transport)
		if transportType != "*retryhttp.Transport" {
			t.Errorf("client for %q: Transport type = %s, want *retryhttp.Transport", source, transportType)
		}
	}

	// Extra verification: all transports are retryhttp and should have consistent config
	transportTypes := make(map[string]int)
	for _, client := range clients {
		poclient := client.(*ProductOpenerClient)
		tType := fmt.Sprintf("%T", poclient.httpClient.Transport)
		transportTypes[tType]++
	}

	if len(transportTypes) != 1 {
		t.Errorf("expected all clients to have the same transport type, got %v", transportTypes)
	}
}

func TestProductOpenerClientAccessors(t *testing.T) {
	source := ExternalSourceOpenFoodFacts
	baseURL := "https://test.example.com/api/v2/product"

	client := NewProductOpenerClient(source, baseURL)

	if client.Source() != source {
		t.Errorf("Source(): want %q, got %q", source, client.Source())
	}

	if client.BaseURL() != baseURL {
		t.Errorf("BaseURL(): want %q, got %q", baseURL, client.BaseURL())
	}
}

func TestExternalSourceValid(t *testing.T) {
	tests := []struct {
		source ExternalSource
		valid  bool
	}{
		{"", true},
		{ExternalSourceOpenFoodFacts, true},
		{ExternalSourceOpenProductsFacts, true},
		{ExternalSourceOpenBeautyFacts, true},
		{ExternalSourceOpenPetFoodFacts, true},
		{ExternalSource("invalid"), false},
		{ExternalSource("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			got := tt.source.Valid()
			if got != tt.valid {
				t.Errorf("Valid() for %q: want %v, got %v", tt.source, tt.valid, got)
			}
		})
	}
}

// TestClientDecodingAcrossDatabases tests wire format round-tripping across all four databases.
// **Validates: Requirements 7.4, 7.5, 7.6**
// **Property 23: The wire format round-trips through the client**
// **Property 24: Three conditions each mean unknown**
// **Property 25: A resolved product's ID is the requested barcode**
func TestClientDecodingAcrossDatabases(t *testing.T) {
	allSources := []ExternalSource{
		ExternalSourceOpenFoodFacts,
		ExternalSourceOpenProductsFacts,
		ExternalSourceOpenBeautyFacts,
		ExternalSourceOpenPetFoodFacts,
	}

	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		barcode      string
		wantErr      error
		checkProduct func(*testing.T, *ProductSummary)
	}{
		{
			name:       "success with all fields",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "Test Product",
					"categories": "Beverages,Drinks",
					"code": "123456789012",
					"image_front_small_url": "https://example.com/image.jpg"
				}
			}`,
			barcode: "123456789012",
			checkProduct: func(t *testing.T, ps *ProductSummary) {
				if ps == nil {
					t.Fatal("expected ProductSummary, got nil")
				}
				if ps.Name != "Test Product" {
					t.Errorf("expected name 'Test Product', got %q", ps.Name)
				}
				if ps.Category != "Beverages,Drinks" {
					t.Errorf("expected category 'Beverages,Drinks', got %q", ps.Category)
				}
				if ps.ImageURL != "https://example.com/image.jpg" {
					t.Errorf("expected ImageURL, got %q", ps.ImageURL)
				}
				if ps.UnitOfMeasure != "" {
					t.Errorf("expected empty UnitOfMeasure, got %q", ps.UnitOfMeasure)
				}
				// Property 25: product ID is the requested barcode
				if ps.ID != "123456789012" {
					t.Errorf("Property 25 violated: expected ID '123456789012', got %q", ps.ID)
				}
			},
		},
		{
			name:       "success without image",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "Minimal Product",
					"categories": "",
					"code": "987654321098"
				}
			}`,
			barcode: "987654321098",
			checkProduct: func(t *testing.T, ps *ProductSummary) {
				if ps == nil {
					t.Fatal("expected ProductSummary, got nil")
				}
				if ps.ImageURL != "" {
					t.Errorf("expected empty ImageURL, got %q", ps.ImageURL)
				}
				if ps.Category != "" {
					t.Errorf("expected empty Category, got %q", ps.Category)
				}
				// Property 25: ID is requested barcode, not response code
				if ps.ID != "987654321098" {
					t.Errorf("Property 25 violated: expected ID '987654321098', got %q", ps.ID)
				}
			},
		},
		{
			name:       "success with response code differing from barcode",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "Product With Code",
					"categories": "Test",
					"code": "999999999999",
					"image_front_small_url": "https://example.com/prod.jpg"
				}
			}`,
			barcode: "111111111111",
			checkProduct: func(t *testing.T, ps *ProductSummary) {
				if ps == nil {
					t.Fatal("expected ProductSummary, got nil")
				}
				// Property 25: ID must be the requested barcode, not the response code
				if ps.ID != "111111111111" {
					t.Errorf("Property 25 violated: expected ID '111111111111' (requested), got %q", ps.ID)
				}
				if ps.Name != "Product With Code" {
					t.Errorf("expected name 'Product With Code', got %q", ps.Name)
				}
			},
		},
		{
			name:       "miss - HTTP 404",
			statusCode: http.StatusNotFound,
			barcode:    "404404404404",
			wantErr:    ErrProductNotFound,
		},
		{
			name:       "miss - status != 1",
			statusCode: 200,
			responseBody: `{
				"status": 0,
				"product": {
					"product_name": "Should Not Match",
					"categories": "Test"
				}
			}`,
			barcode: "555555555555",
			wantErr: ErrProductNotFound,
		},
		{
			name:       "miss - empty product_name",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "",
					"categories": "Test"
				}
			}`,
			barcode: "666666666666",
			wantErr: ErrProductNotFound,
		},
		{
			name:       "miss - status 2",
			statusCode: 200,
			responseBody: `{
				"status": 2,
				"product": {
					"product_name": "Real Name",
					"categories": "Test"
				}
			}`,
			barcode: "777777777777",
			wantErr: ErrProductNotFound,
		},
		{
			name:         "malformed JSON",
			statusCode:   200,
			responseBody: `not valid json`,
			barcode:      "888888888888",
			wantErr:      errors.New("failed to decode"),
		},
		{
			name:         "server error 500",
			statusCode:   http.StatusInternalServerError,
			responseBody: `{"error": "server error"}`,
			barcode:      "999999999999",
			wantErr:      errors.New("status 500"),
		},
	}

	// Run all tests against all four databases
	for _, source := range allSources {
		for _, tt := range tests {
			testName := tt.name + "_" + string(source)
			t.Run(testName, func(t *testing.T) {
				// Create a mock HTTP server for this database
				mock := mockTransport{
					fn: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: tt.statusCode,
							Body:       io.NopCloser(bytes.NewReader([]byte(tt.responseBody))),
							Header:     make(http.Header),
						}, nil
					},
				}

				baseURL := "https://test.example.com/api/v2/product"
				client := NewProductOpenerClientWithHTTPClient(source, baseURL, &http.Client{
					Timeout:   10 * time.Second,
					Transport: mock,
				})

				ps, err := client.LookupBarcode(context.Background(), tt.barcode)

				// Check error expectation
				if tt.wantErr != nil {
					if err == nil {
						t.Fatalf("expected error %v, got nil", tt.wantErr)
					}
					if !errors.Is(err, tt.wantErr) && !strings.Contains(err.Error(), tt.wantErr.Error()) {
						t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
					}
					if ps != nil {
						t.Errorf("expected nil ProductSummary on error, got %v", ps)
					}
					return
				}

				// Check success
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if tt.checkProduct != nil {
					tt.checkProduct(t, ps)
				}
			})
		}
	}
}

// TestMissConditionsAcrossDatabases specifically validates the three miss conditions.
// **Validates: Requirements 7.5, 7.6**
// **Property 24: Three conditions each mean unknown**
func TestMissConditionsAcrossDatabases(t *testing.T) {
	allSources := []ExternalSource{
		ExternalSourceOpenFoodFacts,
		ExternalSourceOpenProductsFacts,
		ExternalSourceOpenBeautyFacts,
		ExternalSourceOpenPetFoodFacts,
	}

	missConditions := []struct {
		name         string
		statusCode   int
		responseBody string
		description  string
	}{
		{
			name:        "HTTP 404",
			statusCode:  http.StatusNotFound,
			description: "Not Found response indicates unknown barcode",
		},
		{
			name:       "status field != 1",
			statusCode: 200,
			responseBody: `{
				"status": 0,
				"product": {
					"product_name": "Real Name",
					"categories": "Real Category"
				}
			}`,
			description: "status != 1 indicates unknown to that database",
		},
		{
			name:       "empty product_name",
			statusCode: 200,
			responseBody: `{
				"status": 1,
				"product": {
					"product_name": "",
					"categories": "Has Category"
				}
			}`,
			description: "empty product_name indicates unknown product",
		},
	}

	for _, source := range allSources {
		for _, condition := range missConditions {
			testName := "source_" + string(source) + "_" + condition.name
			t.Run(testName, func(t *testing.T) {
				mock := mockTransport{
					fn: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: condition.statusCode,
							Body:       io.NopCloser(bytes.NewReader([]byte(condition.responseBody))),
							Header:     make(http.Header),
						}, nil
					},
				}

				client := NewProductOpenerClientWithHTTPClient(source, "https://test.example.com/api/v2/product", &http.Client{
					Timeout:   10 * time.Second,
					Transport: mock,
				})

				ps, err := client.LookupBarcode(context.Background(), "123456789012")

				// All three conditions must yield ErrProductNotFound
				if !errors.Is(err, ErrProductNotFound) {
					t.Errorf("Property 24 violated: %s, expected ErrProductNotFound, got %v", condition.description, err)
				}
				if ps != nil {
					t.Errorf("expected nil ProductSummary, got %v", ps)
				}
			})
		}
	}
}

// TestProductIDInvariantAcrossDatabases validates that product ID is always the requested barcode.
// **Validates: Requirements 7.6**
// **Property 25: A resolved product's ID is the requested barcode**
func TestProductIDInvariantAcrossDatabases(t *testing.T) {
	allSources := []ExternalSource{
		ExternalSourceOpenFoodFacts,
		ExternalSourceOpenProductsFacts,
		ExternalSourceOpenBeautyFacts,
		ExternalSourceOpenPetFoodFacts,
	}

	testCases := []struct {
		requestedBarcode string
		responseCode     string
		description      string
	}{
		{
			requestedBarcode: "111111111111",
			responseCode:     "111111111111",
			description:      "request and response barcodes match",
		},
		{
			requestedBarcode: "222222222222",
			responseCode:     "999999999999",
			description:      "response code differs from requested barcode",
		},
		{
			requestedBarcode: "333333333333",
			responseCode:     "444444444444",
			description:      "completely different code in response",
		},
	}

	for _, source := range allSources {
		for _, tc := range testCases {
			testName := "source_" + string(source) + "_" + tc.description
			t.Run(testName, func(t *testing.T) {
				responseBody := fmt.Sprintf(`{
					"status": 1,
					"product": {
						"product_name": "Test Product",
						"categories": "Test Category",
						"code": "%s",
						"image_front_small_url": "https://example.com/image.jpg"
					}
				}`, tc.responseCode)

				mock := mockTransport{
					fn: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(bytes.NewReader([]byte(responseBody))),
							Header:     make(http.Header),
						}, nil
					},
				}

				client := NewProductOpenerClientWithHTTPClient(source, "https://test.example.com/api/v2/product", &http.Client{
					Timeout:   10 * time.Second,
					Transport: mock,
				})

				ps, err := client.LookupBarcode(context.Background(), tc.requestedBarcode)

				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Property 25: The ID must always be the requested barcode, never the response code
				if ps.ID != tc.requestedBarcode {
					t.Errorf("Property 25 violated (%s): expected ID %q, got %q", tc.description, tc.requestedBarcode, ps.ID)
				}
			})
		}
	}
}

func TestLookupBarcodeKeepsBarcodeInOnePathSegment(t *testing.T) {
	var gotPath string
	client := NewProductOpenerClientWithHTTPClient(ExternalSourceOpenFoodFacts, "https://world.openfoodfacts.org/api/v2/product", &http.Client{
		Transport: mockTransport{fn: func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}},
	})

	if _, err := client.LookupBarcode(context.Background(), "012345678905"); err != ErrProductNotFound {
		t.Fatalf("lookup: %v", err)
	}
	if gotPath != "/api/v2/product/012345678905.json" {
		t.Fatalf("path = %q", gotPath)
	}

	for _, barcode := range []string{"../admin", "a/b", "a?b=1", "has space trailing ", "\n"} {
		if _, err := client.LookupBarcode(context.Background(), barcode); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("barcode %q: got %v, want invalid", barcode, err)
		}
	}
}

func TestLookupBarcodeReadsBrand(t *testing.T) {
	client := NewProductOpenerClientWithHTTPClient(ExternalSourceOpenFoodFacts, "https://world.openfoodfacts.org/api/v2/product", &http.Client{
		Transport: mockTransport{fn: func(req *http.Request) (*http.Response, error) {
			body := `{"status":1,"product":{"product_name":"Sharp Cheddar","brands":"Tillamook"}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}},
	})
	ps, err := client.LookupBarcode(context.Background(), "012345678905")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ps.Brand != "Tillamook" {
		t.Fatalf("Brand = %q, want Tillamook", ps.Brand)
	}
}

func TestLookupBarcodeDropsPrivateImageURL(t *testing.T) {
	client := NewProductOpenerClientWithHTTPClient(ExternalSourceOpenFoodFacts, "https://world.openfoodfacts.org/api/v2/product", &http.Client{
		Transport: mockTransport{fn: func(req *http.Request) (*http.Response, error) {
			body := `{"status":1,"product":{"product_name":"Milk","image_front_small_url":"http://192.168.1.1/cam.jpg"}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}},
	})
	ps, err := client.LookupBarcode(context.Background(), "012345678905")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ps.ImageURL != "" {
		t.Fatalf("ImageURL = %q, want empty", ps.ImageURL)
	}
}

func TestLookupBarcodeRefusesCrossHostRedirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/secret", http.StatusFound)
	}))
	t.Cleanup(upstream.Close)

	client := NewProductOpenerClient(ExternalSourceOpenFoodFacts, upstream.URL+"/api/v2/product")
	_, err := client.LookupBarcode(context.Background(), "012345678905")
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("lookup error = %v, want a redirect refusal", err)
	}
}
