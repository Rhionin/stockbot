package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scanlistener"
	"github.com/Rhionin/stockbot/internal/server"
	"github.com/SherClockHolmes/webpush-go"
	"golang.org/x/crypto/bcrypt"
)

func TestDurationAndBatchDefaults(t *testing.T) {
	tests := []struct {
		name string
		key  string
		raw  string
		want time.Duration
		read func() time.Duration
	}{
		{name: "cache default", key: "PRODUCT_CACHE_TTL", want: defaultProductCacheTTL, read: productCacheTTL},
		{name: "cache parsed", key: "PRODUCT_CACHE_TTL", raw: "1h", want: time.Hour, read: productCacheTTL},
		{name: "cache zero", key: "PRODUCT_CACHE_TTL", raw: "0s", want: 0, read: productCacheTTL},
		{name: "cache invalid", key: "PRODUCT_CACHE_TTL", raw: "nope", want: defaultProductCacheTTL, read: productCacheTTL},
		{name: "miss default", key: "PRODUCT_MISS_TTL", want: defaultMissTTL, read: productMissTTL},
		{name: "miss parsed", key: "PRODUCT_MISS_TTL", raw: "1m", want: time.Minute, read: productMissTTL},
		{name: "miss zero", key: "PRODUCT_MISS_TTL", raw: "0s", want: 0, read: productMissTTL},
		{name: "miss invalid", key: "PRODUCT_MISS_TTL", raw: "nope", want: defaultMissTTL, read: productMissTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.raw)
			if got := tt.read(); got != tt.want {
				t.Fatalf("%s = %s, want %s", tt.key, got, tt.want)
			}
		})
	}

	batches := []struct {
		raw  string
		want int
	}{
		{raw: "", want: 50},
		{raw: "12", want: 12},
		{raw: "0", want: 50},
		{raw: "1000", want: 50},
		{raw: "nope", want: 50},
	}
	for _, tt := range batches {
		t.Run("batch "+tt.raw, func(t *testing.T) {
			t.Setenv("KROGER_BATCH_SIZE", tt.raw)
			if got := krogerBatchSize(); got != tt.want {
				t.Fatalf("batch %q = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}

	t.Setenv("ADDR", "")
	if envOrDefault("ADDR", ":8080") != ":8080" {
		t.Fatal("empty ADDR should use the default")
	}
	t.Setenv("ADDR", ":9090")
	if envOrDefault("ADDR", ":8080") != ":9090" {
		t.Fatal("ADDR should keep an explicit value")
	}
}

func TestDisabledExternalLookup(t *testing.T) {
	got := disabledExternalLookup{}.Lookup(context.Background(), "000")
	if got.Outcome != product.FanOutUnresolved {
		t.Fatalf("outcome %v", got.Outcome)
	}
	summary, err := disabledExternalLookup{}.LookupIn(context.Background(), product.ExternalSourceOpenFoodFacts, "000")
	if summary != nil || !errors.Is(err, product.ErrProductNotFound) {
		t.Fatalf("summary %v err %v", summary, err)
	}
}

func TestScanListenerConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("STOCK_IN_CONTROL_BARCODE", "")
		t.Setenv("STOCK_OUT_CONTROL_BARCODE", "")
		t.Setenv("SCAN_INPUT", "")
		t.Setenv("SCANNER_DEVICE", "")
		listener, ok := loadScanListenerConfig()
		if !ok || listener.StockInBarcode != "STOCK_IN" || listener.StockOutBarcode != "STOCK_OUT" {
			t.Fatalf("listener %+v ok %v", listener, ok)
		}
		withSource, ok := loadScanListenerConfigWithSource()
		if !ok || withSource.Source != scanlistener.SourceDevice || withSource.DevicePath != "/dev/input/pantry-scanner" {
			t.Fatalf("sourced %+v ok %v", withSource, ok)
		}
	})
	t.Run("stdin and custom device", func(t *testing.T) {
		t.Setenv("SCAN_INPUT", "stdin")
		t.Setenv("SCANNER_DEVICE", "/tmp/scanner")
		t.Setenv("STOCK_IN_CONTROL_BARCODE", "IN")
		t.Setenv("STOCK_OUT_CONTROL_BARCODE", "OUT")
		listener, ok := loadScanListenerConfigWithSource()
		if !ok || listener.Source != scanlistener.SourceStdin || listener.DevicePath != "/tmp/scanner" {
			t.Fatalf("listener %+v ok %v", listener, ok)
		}
	})
	t.Run("invalid source still starts", func(t *testing.T) {
		t.Setenv("SCAN_INPUT", "camera")
		t.Setenv("STOCK_IN_CONTROL_BARCODE", "IN")
		t.Setenv("STOCK_OUT_CONTROL_BARCODE", "OUT")
		listener, ok := loadScanListenerConfigWithSource()
		if !ok || listener.Source != scanlistener.SourceDevice {
			t.Fatalf("listener %+v ok %v", listener, ok)
		}
	})
	t.Run("identical control barcodes do not start", func(t *testing.T) {
		t.Setenv("STOCK_IN_CONTROL_BARCODE", "SAME")
		t.Setenv("STOCK_OUT_CONTROL_BARCODE", "SAME")
		if _, ok := loadScanListenerConfig(); ok {
			t.Fatal("matching barcodes started the deprecated listener")
		}
		if _, ok := loadScanListenerConfigWithSource(); ok {
			t.Fatal("matching barcodes started the listener")
		}
	})
}

func TestLoadPushAndHousehold(t *testing.T) {
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	if loadPushConfig().Configured() {
		t.Fatal("missing VAPID keys should leave push off")
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("vapid: %v", err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", public)
	t.Setenv("VAPID_PRIVATE_KEY", "not-a-key")
	if loadPushConfig().Configured() {
		t.Fatal("a partial VAPID pair should leave push off")
	}
	t.Setenv("VAPID_PRIVATE_KEY", private)
	if !loadPushConfig().Configured() {
		t.Fatal("a VAPID pair should turn push on")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse-battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	path := filepath.Join(t.TempDir(), "household")
	body := "basic_auth bcrypt Pantry {\n\tpantry " + string(hash) + "\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PANTRY_AUTH_FILE", path)
	t.Setenv("PANTRY_SESSION_SECRET", "")
	if gate := loadHouseholdAuth(); gate == nil || !gate.Configured || gate.Username != "pantry" {
		t.Fatalf("login without a session secret = %+v", gate)
	}
	t.Setenv("PANTRY_SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	if gate := loadHouseholdAuth(); gate == nil || !gate.Configured || gate.Username != "pantry" {
		t.Fatalf("configured login = %+v", gate)
	}
}

func TestConfiguredStartupServesHealth(t *testing.T) {
	t.Setenv("PRODUCT_CACHE_TTL", "1h")
	t.Setenv("PRODUCT_MISS_TTL", "1m")
	t.Setenv("DISABLE_KROGER", "true")
	t.Setenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP", "true")
	t.Setenv("PANTRY_AUTH_FILE", "")
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("STOCK_IN_CONTROL_BARCODE", "IN")
	t.Setenv("STOCK_OUT_CONTROL_BARCODE", "OUT")
	t.Setenv("SCAN_INPUT", "stdin")
	db := newCartDB(t)
	registry, ledger, providerEnv := loadCartRegistry(db)
	listener, ok := loadScanListenerConfigWithSource()
	if !ok || listener.Source != scanlistener.SourceStdin {
		t.Fatalf("listener %+v ok %v", listener, ok)
	}
	catalog := product.NewCatalog(db)
	upstream := disabledExternalLookup{}
	refresher := &product.Refresher{
		Catalog: catalog, Upstream: upstream, TTL: productCacheTTL(), ExternalLookupEnabled: false,
	}
	lookup := &product.LookupService{
		Catalog: catalog, Upstream: upstream, Refresher: refresher, MissTTL: productMissTTL(),
	}
	handler, _ := server.NewHandler(catalog, lookup, refresher, db,
		server.WithCartRegistry(registry, ledger),
		server.WithProviderEnv(providerEnv),
		server.WithHouseholdAuth(loadHouseholdAuth()),
		server.WithPush(loadPushConfig()),
		server.WithScannerConfig(server.ScannerConfig{StockInBarcode: listener.StockInBarcode, StockOutBarcode: listener.StockOutBarcode}),
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health %d %s", rec.Code, rec.Body.String())
	}
	if productCacheTTL() != time.Hour || productMissTTL() != time.Minute {
		t.Fatal("startup did not keep the configured TTLs")
	}
}
