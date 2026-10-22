// Package product provides product and barcode management functionality.
package product

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/justinrixx/retryhttp"
)

var ErrProductNotFound = errors.New("product not found")

// ExternalSource identifies which Product Opener database supplied a product's
// field values. The empty value means "not externally sourced, or sourced
// before provenance was recorded".
type ExternalSource string

const (
	ExternalSourceOpenFoodFacts     ExternalSource = "openfoodfacts"
	ExternalSourceOpenProductsFacts ExternalSource = "openproductsfacts"
	ExternalSourceOpenBeautyFacts   ExternalSource = "openbeautyfacts"
	ExternalSourceOpenPetFoodFacts  ExternalSource = "openpetfoodfacts"
)

// Valid reports whether s is storable. The empty value is valid and means
// "no external provenance". SQLite cannot enforce this set with a CHECK
// constraint added via ALTER TABLE, so CreateProduct enforces it in Go,
// exactly as it does for products.source.
func (s ExternalSource) Valid() bool {
	switch s {
	case "", ExternalSourceOpenFoodFacts, ExternalSourceOpenProductsFacts,
		ExternalSourceOpenBeautyFacts, ExternalSourceOpenPetFoodFacts:
		return true
	}
	return false
}

// productOpenerBaseURLs maps each database to its API root. Adding a fifth
// Product Opener database is an entry here plus an entry in databasePrecedence.
var productOpenerBaseURLs = map[ExternalSource]string{
	ExternalSourceOpenFoodFacts:     "https://world.openfoodfacts.org/api/v2/product",
	ExternalSourceOpenProductsFacts: "https://world.openproductsfacts.org/api/v2/product",
	ExternalSourceOpenBeautyFacts:   "https://world.openbeautyfacts.org/api/v2/product",
	ExternalSourceOpenPetFoodFacts:  "https://world.openpetfoodfacts.org/api/v2/product",
}

// databasePrecedence resolves a barcode present in more than one upstream
// database. The fan-out iterates this slice and returns the first hit, so
// changing the ordering is changing this literal — there is no branching
// logic to find. Ordered by expected pantry-scan frequency.
var databasePrecedence = []ExternalSource{
	ExternalSourceOpenFoodFacts,
	ExternalSourceOpenProductsFacts,
	ExternalSourceOpenBeautyFacts,
	ExternalSourceOpenPetFoodFacts,
}

// ProductOpenerClient queries one Product Opener database. All four databases
// serve the same read API shape and differ only in host, so one type with a
// construction-time base URL covers all of them.
type ProductOpenerClient struct {
	source     ExternalSource
	baseURL    string
	httpClient *http.Client
}

// NewProductOpenerClient builds a client for one database with the standard
// retry transport and a 10 second timeout.
func NewProductOpenerClient(source ExternalSource, baseURL string) *ProductOpenerClient {
	transport := retryhttp.New(
		retryhttp.WithShouldRetryFn(shouldRetryIncluding429),
	)

	return &ProductOpenerClient{
		source:  source,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
			// A 3xx from the product database must not turn a barcode lookup
			// into a request to some other host.
			CheckRedirect: refuseCrossHostRedirect,
		},
	}
}

// refuseCrossHostRedirect stops a product-database redirect from leaving the
// host the client was built to call.
func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return fmt.Errorf("refusing redirect to a different host")
	}
	if len(via) >= 3 {
		return fmt.Errorf("stopped after several redirects")
	}
	return nil
}

// NewProductOpenerClientWithHTTPClient builds a client with a caller-supplied
// HTTP client, for tests that stand a fake server in front of a database.
func NewProductOpenerClientWithHTTPClient(source ExternalSource, baseURL string, hc *http.Client) *ProductOpenerClient {
	return &ProductOpenerClient{
		source:     source,
		baseURL:    baseURL,
		httpClient: hc,
	}
}

// Source reports which database this client queries.
func (c *ProductOpenerClient) Source() ExternalSource {
	return c.source
}

// BaseURL reports the URL this client queries, so a test can confirm a fake
// server stands in for the intended database.
func (c *ProductOpenerClient) BaseURL() string {
	return c.baseURL
}

// DefaultProductOpenerClients builds one client per database, each with the
// same retry transport and timeout.
func DefaultProductOpenerClients() map[ExternalSource]BarcodeLookup {
	clients := make(map[ExternalSource]BarcodeLookup)
	for source, baseURL := range productOpenerBaseURLs {
		clients[source] = NewProductOpenerClient(source, baseURL)
	}
	return clients
}

// productOpenerResponse represents the JSON response structure from Product Opener API.
type productOpenerResponse struct {
	Product struct {
		Name                string    `json:"product_name"`
		Brands              string    `json:"brands"`
		Category            string    `json:"categories"`
		Code                string    `json:"code"`
		ImageThumbURL       string    `json:"image_front_small_url"`
		Quantity            string    `json:"quantity"`
		ProductQuantity     flexFloat `json:"product_quantity"`
		ProductQuantityUnit string    `json:"product_quantity_unit"`
		CategoriesTags      []string  `json:"categories_tags"`
		FoodGroupsTags      []string  `json:"food_groups_tags"`
	} `json:"product"`
	Status int `json:"status"`
}

// flexFloat accepts the number Open Food Facts usually sends and the string
// it sends for some products.
type flexFloat struct {
	Value float64
	Set   bool
}

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		return nil
	}
	var number float64
	if err := stdjson.Unmarshal(data, &number); err == nil {
		f.Value = number
		f.Set = true
		return nil
	}
	var text string
	if err := stdjson.Unmarshal(data, &text); err != nil {
		return err
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, ",", "."))
	if text == "" {
		return nil
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	f.Value = number
	f.Set = true
	return nil
}

// LookupBarcode looks up a product by barcode from a Product Opener database.
// Uses retryhttp with exponential backoff for rate-limit (429) and transient errors.
//
// Returns:
//   - (*ProductSummary, nil) if product is found
//   - (nil, ErrProductNotFound) if product is not found (404 or invalid response)
//   - (nil, error) if an error occurs
//
// ProductSummary.ID is set from the requested barcode, never from the response's
// code field. This preserves the invariant that an external row's product ID is
// its barcode.
func (c *ProductOpenerClient) LookupBarcode(ctx context.Context, barcode string) (*ProductSummary, error) {
	if barcode == "" {
		return nil, fmt.Errorf("barcode cannot be empty")
	}
	if !safeBarcodePath(barcode) {
		return nil, fmt.Errorf("barcode is invalid")
	}

	// PathEscape keeps the barcode in one path segment. A slash, question
	// mark, or ".." would otherwise change which URL the server requests.
	endpoint := strings.TrimRight(c.baseURL, "/") + "/" + url.PathEscape(barcode) + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrProductNotFound
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body is not part of the error. It can be large, and it is not
		// something to log or return to a caller.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("Product Opener database returned status %d", resp.StatusCode)
	}

	var data productOpenerResponse
	if err := json.UnmarshalRead(resp.Body, &data); err != nil {
		return nil, fmt.Errorf("failed to decode Product Opener response: %w", err)
	}

	if data.Status != 1 || data.Product.Name == "" {
		return nil, ErrProductNotFound
	}

	ps := &ProductSummary{
		ID:             barcode,
		Name:           data.Product.Name,
		Brand:          strings.TrimSpace(data.Product.Brands),
		Category:       data.Product.Category,
		UnitOfMeasure:  "",
		ImageURL:       SafeImageURL(data.Product.ImageThumbURL),
		CategoriesTags: data.Product.CategoriesTags,
		FoodGroupsTags: data.Product.FoodGroupsTags,
	}
	quantity := 0.0
	if data.Product.ProductQuantity.Set {
		quantity = data.Product.ProductQuantity.Value
	}
	if parsed, ok := NetSizeFromOpenFoodFacts(data.Product.Quantity, quantity, data.Product.ProductQuantityUnit); ok {
		base := parsed.BaseValue
		ps.NetBaseValue = &base
		ps.NetDimension = parsed.Dimension
		if parsed.HasPack {
			pack := parsed.PackCount
			ps.PackCount = &pack
		}
	}
	return ps, nil
}

// maxBarcodeLen is longer than a GTIN and shorter than a URL. Control barcodes
// such as STOCK_IN fit; a value this long is not a barcode a scanner emits.
const maxBarcodeLen = 128

// safeBarcodePath reports whether barcode can be placed in one URL path
// segment without changing the request target.
func safeBarcodePath(barcode string) bool {
	if barcode == "" || len(barcode) > maxBarcodeLen {
		return false
	}
	if strings.ContainsAny(barcode, "/?#\\\r\n\t") {
		return false
	}
	return strings.TrimSpace(barcode) == barcode
}

// shouldRetryIncluding429 wraps the default retry logic and adds 429 (rate-limit) handling.
func shouldRetryIncluding429(attempt retryhttp.Attempt) bool {
	if attempt.Res != nil && attempt.Res.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return retryhttp.DefaultShouldRetryFn(attempt)
}
