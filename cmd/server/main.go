package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/push"
	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/scanlistener"
	"github.com/Rhionin/stockbot/internal/server"
)

// defaultProductCacheTTL is used when PRODUCT_CACHE_TTL is unset, empty, or
// unparseable.
const defaultProductCacheTTL = 30 * 24 * time.Hour

// defaultMissTTL is used when PRODUCT_MISS_TTL is unset, empty, or unparseable.
const defaultMissTTL = 7 * 24 * time.Hour

// productCacheTTL reads PRODUCT_CACHE_TTL and returns the TTL a product
// cache row uses before Refresher.ScheduleRefresh considers it stale. An
// unparseable value is logged and defaulted rather than failing startup. A
// value that does parse is used as given even if non-positive: 0s is a
// legitimate "always revalidate" debugging setting, and the Refresher's
// single-flight guard keeps it from stampeding upstream.
func productCacheTTL() time.Duration {
	raw := os.Getenv("PRODUCT_CACHE_TTL")
	if raw == "" {
		return defaultProductCacheTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("invalid PRODUCT_CACHE_TTL %q, using default of %s", raw, defaultProductCacheTTL)
		return defaultProductCacheTTL
	}
	return ttl
}

// productMissTTL reads PRODUCT_MISS_TTL and returns the age at which a
// confirmed-miss record expires and the barcode becomes eligible for another
// fan-out. Shaped exactly like productCacheTTL: an unparseable value is logged
// and defaulted rather than failing startup, and a value that parses is used as
// given even if non-positive, since 0s is a legitimate "never cache a miss"
// debugging setting.
func productMissTTL() time.Duration {
	raw := os.Getenv("PRODUCT_MISS_TTL")
	if raw == "" {
		return defaultMissTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("invalid PRODUCT_MISS_TTL %q, using default of %s", raw, defaultMissTTL)
		return defaultMissTTL
	}
	return ttl
}

// loadCartRegistry reads Kroger's environment and any credentials saved from
// the Pantry UI. A saved row overrides the environment until it is cleared.
// Missing credentials register Kroger as not configured so the shopping list
// can say so; they do not stop the process.
func loadCartRegistry(db *sql.DB) (*cart.Registry, *cart.Ledger, server.ProviderEnv) {
	registry := cart.NewRegistry()
	ledger := cart.NewLedger(db)

	disabled := strings.TrimSpace(os.Getenv("DISABLE_KROGER")) == "true"
	clientID := strings.TrimSpace(os.Getenv("KROGER_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("KROGER_CLIENT_SECRET"))
	redirectURI := strings.TrimSpace(os.Getenv("KROGER_REDIRECT_URI"))
	if disabled {
		log.Println("Kroger provider disabled via DISABLE_KROGER")
	}

	configured := !disabled && clientID != "" && clientSecret != "" && redirectURI != ""
	envCreds := server.ProviderEnv{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Disabled:     disabled,
	}
	modality := strings.ToUpper(strings.TrimSpace(os.Getenv("KROGER_MODALITY")))
	if modality == "" {
		modality = "PICKUP"
	} else if modality != "PICKUP" && modality != "DELIVERY" {
		log.Printf("invalid KROGER_MODALITY %q, using default PICKUP", modality)
		modality = "PICKUP"
	}
	envCreds.Modality = modality

	var adapter *kroger.Adapter
	if configured {
		created, err := kroger.New(clientID, clientSecret, redirectURI, modality)
		if err != nil {
			log.Printf("failed to create Kroger adapter: %v", err)
			configured = false
			adapter = kroger.NewUnconfigured()
		} else {
			adapter = created
		}
	} else {
		adapter = kroger.NewUnconfigured()
	}

	if !disabled {
		saved, ok, err := appcred.NewVault(db).Load(context.Background(), "kroger")
		if err != nil {
			log.Printf("read saved Kroger credentials: %v", err)
		} else if ok {
			if err := adapter.ApplyAppCredentials(saved.ClientID, saved.ClientSecret, saved.RedirectURI, saved.Modality); err != nil {
				log.Printf("saved Kroger credentials were not applied: %v", err)
			} else {
				configured = true
			}
		}
	}

	opts := []cart.RegisterOption{
		cart.WithBatchSize(krogerBatchSize()),
		cart.WithCredentialsConfigured(configured),
	}
	if err := registry.Register(adapter, opts...); err != nil {
		log.Printf("failed to register Kroger: %v", err)
	}
	if !configured && !disabled {
		log.Println("Kroger credentials are not configured; set them in the shopping list or with KROGER_CLIENT_ID, KROGER_CLIENT_SECRET, and KROGER_REDIRECT_URI")
	}
	return registry, ledger, envCreds
}

func krogerBatchSize() int {
	raw := strings.TrimSpace(os.Getenv("KROGER_BATCH_SIZE"))
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 999 {
		log.Printf("invalid KROGER_BATCH_SIZE %q, using default 50", raw)
		return 50
	}
	return n
}

func main() {
	dbPath := envOrDefault("DB_PATH", "pantry.db")
	site, err := app.LoadRuntimeConfig(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if site.AppBaseURL == "" {
		log.Printf("site: %s, LAN only", site.AppName)
	} else {
		log.Printf("site: %s at %s", site.AppName, site.AppBaseURL)
	}

	sqlDB, err := app.OpenSQLite(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	// SQLite is single-writer; limit to one connection to avoid SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)

	// Bind before migrations. /health is 503 with status migrating until the
	// real handler is installed, and /api/build is already this binary.
	// ReadHeaderTimeout closes a connection that never sends headers.
	// WriteTimeout is left unset so a live /api/events stream is not cut off.
	gate := newBootGate()
	streams := server.NewStreamShutdown()
	// Shutdown does not cancel request contexts. Register ends SSE handlers
	// so a live /api/events tab does not hold the process for the whole budget.
	listeners, err := openListeners(site.Listeners(), gate, streams.Register)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	if err := app.RunMigrations(sqlDB); err != nil {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("migrations applied")

	if _, err := product.BackfillNetSizes(context.Background(), sqlDB); err != nil {
		log.Fatalf("backfill product sizes: %v", err)
	}
	if err := group.NewGroups(sqlDB).Seed(context.Background()); err != nil {
		log.Fatalf("seed group suggestions: %v", err)
	}

	catalog := product.NewCatalog(sqlDB)
	externalLookupEnabled := os.Getenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP") != "true"
	var upstream product.UpstreamDatabases = product.NewExternalLookup(product.DefaultProductOpenerClients())
	if !externalLookupEnabled {
		upstream = disabledExternalLookup{}
	}
	restockTargets := restock.NewTargets(sqlDB)
	refresher := &product.Refresher{
		Catalog:               catalog,
		Upstream:              upstream,
		TTL:                   productCacheTTL(),
		ExternalLookupEnabled: externalLookupEnabled,
		Tags:                  restockTargets,
	}
	lookupService := &product.LookupService{
		Catalog:   catalog,
		Upstream:  upstream,
		Refresher: refresher,
		MissTTL:   productMissTTL(),
		Tags:      restockTargets,
	}

	// One Broadcaster shared by the HTTP handlers and the headless listener, so
	// a mode change from either capture path reaches the same GET /api/events
	// subscribers.
	broadcaster := events.NewBroadcaster()

	// Load cart registry and register Kroger adapter if configured.
	registry, ledger, providerEnv := loadCartRegistry(sqlDB)

	stockInBarcode := envOrDefault("STOCK_IN_CONTROL_BARCODE", "STOCK_IN")
	stockOutBarcode := envOrDefault("STOCK_OUT_CONTROL_BARCODE", "STOCK_OUT")

	listener, listenerOK := loadScanListenerConfigWithSource()

	// One mode for the HTTP switch and the headless listener. It publishes
	// scanner_mode, including the idle return to scan-out, and keeps the
	// health status on the same direction the next scan will use.
	scannerMode := server.NewScannerMode(func(d scan.ScanDirection) {
		broadcaster.PublishScannerModeEvent(d)
		if listenerOK {
			listener.SetStatusMode(d)
		}
	})

	// Sharing stays local until an account is configured. The in-app switch
	// still defaults off, so credentials alone never send a product.
	contributor := product.NewContributorFromEnv(os.Getenv)
	if contributor.Configured() {
		log.Println("product contribution: signed in to Product Opener; products are sent only when someone opts in")
	} else {
		log.Println("product contribution: not signed in; opted-in products stay in this pantry")
	}

	sentrySettings := server.LoadSentrySettings(os.Getenv)
	if err := server.InitSentry(sentrySettings); err != nil || sentrySettings.DSN == "" {
		log.Print("sentry: error reporting is off")
	} else {
		log.Print("sentry: error reporting is on")
	}

	pushCfg := loadPushConfig()
	pushCfg.AppName = site.AppName
	opts := []server.Option{
		server.WithBroadcaster(broadcaster),
		server.WithSentryTunnel(sentrySettings.TunnelConfig()),
		server.WithStreamShutdown(streams),
		server.WithScannerConfig(server.ScannerConfig{
			StockInBarcode:  stockInBarcode,
			StockOutBarcode: stockOutBarcode,
		}),
		server.WithCartRegistry(registry, ledger),
		server.WithProviderEnv(providerEnv),
		server.WithRetailerDeals(os.Getenv("PANTRY_RETAILER_API_KEY"), os.Getenv("PANTRY_RETAILER_API_URL")),
		server.WithContributor(contributor),
		server.WithScannerMode(scannerMode),
		server.WithDeployHook(os.Getenv("DEPLOY_HOOK_SECRET"), os.Getenv("DEPLOY_TRIGGER_PATH")),
		server.WithSyncToken(os.Getenv("PANTRY_SYNC_TOKEN")),
		server.WithPush(pushCfg),
	}
	if strings.TrimSpace(os.Getenv("DEPLOY_HOOK_SECRET")) == "" {
		log.Println("deploy hook: DEPLOY_HOOK_SECRET is empty; signed deploys are disabled")
	} else {
		log.Println("deploy hook: signature check enabled")
	}
	if strings.TrimSpace(os.Getenv("PANTRY_SYNC_TOKEN")) == "" {
		log.Println("grocy sync: PANTRY_SYNC_TOKEN is empty; the sync snapshot is off")
	} else {
		log.Println("grocy sync: read-only snapshot is enabled")
	}
	if listenerOK {
		opts = append(opts, server.WithScannerStatus(listener.Status))
	}
	opts = append(opts, server.WithHouseholdAuth(loadHouseholdAuth()))
	handler, scanQueue := server.NewHandler(catalog, lookupService, refresher, sqlDB, opts...)
	gate.use(handler)

	if listenerOK {
		listener.Queue = scanQueue
		listener.LookupService = lookupService
		listener.Mode = scannerMode
		listener.ProcessingPublisher = broadcaster
		go listener.Run(context.Background())
	}

	stop := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		close(stop)
	}()
	if err := runHTTPServers(listeners.servers, listeners.serveErr, stop, server.FlushSentry); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// shutdownTimeout is how long Shutdown waits for handlers to return. SSE
// streams are ended by RegisterOnShutdown; anything still open after this is
// closed outright so the process can exit.
var shutdownTimeout = 5 * time.Second

// shutdownHTTP is srv.Shutdown. Tests replace it to observe when it returns.
var shutdownHTTP = func(srv *http.Server, ctx context.Context) error {
	return srv.Shutdown(ctx)
}

// runHTTPServer waits until stop is closed or the listener's Serve returns,
// then flushes error reports. main has already bound the port and started
// Serve, so this does not listen again. A closed stop shuts that listener down.
// Flush runs only after Shutdown has returned.
func runHTTPServer(srv *http.Server, serveErr <-chan error, stop <-chan struct{}, flush func()) error {
	return runHTTPServers([]*http.Server{srv}, serveErr, stop, flush)
}

// loadPushConfig reads the VAPID pair setup stores in the environment.
// Missing or unusable keys leave background alerts off. An open tab still
// notifies itself.
func loadPushConfig() push.Config {
	cfg := push.ConfigFromEnv(os.Getenv)
	switch {
	case cfg.Configured():
		log.Println("scan alerts: web push is on")
	case strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY")) != "" || strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY")) != "":
		log.Println("scan alerts: VAPID keys are set but not usable; background alerts are off")
	default:
		log.Println("scan alerts: web push is off until VAPID keys are set")
	}
	return cfg
}

// loadHouseholdAuth always returns a gate. An unset, empty, or unreadable
// password file fails closed on the public hostname instead of serving it
// with no login. The LAN listener never sends X-Pantry-Entry, so it stays open.
func loadHouseholdAuth() *server.HouseholdAuth {
	path := strings.TrimSpace(os.Getenv("PANTRY_AUTH_FILE"))
	if path == "" {
		log.Print("household login: no password file is configured; the public site refuses requests")
		return server.FailClosedHouseholdAuth()
	}
	household, err := server.LoadHouseholdAuth(path, os.Getenv("PANTRY_SESSION_SECRET"))
	if err != nil {
		log.Printf("household login: %s", err.Error())
	}
	if household == nil {
		return server.FailClosedHouseholdAuth()
	}
	if household.Configured {
		log.Printf("household login: the public site signs in as %s", household.Username)
	}
	return household
}

// unauthenticatedListenWarning reports when addr is reachable beyond this
// process's loopback interface. The LAN listener has no login of its own;
// that is intentional, and a router forward of this port is not.
func unauthenticatedListenWarning(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ""
	}
	return fmt.Sprintf("warning: %s has no application login; it is meant for the LAN. Do not forward this port from the router", addr)
}

type disabledExternalLookup struct{}

func (disabledExternalLookup) Lookup(context.Context, string) product.FanOutResult {
	return product.FanOutResult{Outcome: product.FanOutUnresolved}
}

func (disabledExternalLookup) LookupIn(context.Context, product.ExternalSource, string) (*product.ProductSummary, error) {
	return nil, product.ErrProductNotFound
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// loadScanListenerConfig is deprecated; use loadScanListenerConfigWithSource.
func loadScanListenerConfig() (*scanlistener.ScanListener, bool) {
	stockIn := envOrDefault("STOCK_IN_CONTROL_BARCODE", "STOCK_IN")
	stockOut := envOrDefault("STOCK_OUT_CONTROL_BARCODE", "STOCK_OUT")
	if stockIn == stockOut {
		log.Printf("scan listener: STOCK_IN_CONTROL_BARCODE and STOCK_OUT_CONTROL_BARCODE must differ, not starting scan listener")
		return nil, false
	}

	return &scanlistener.ScanListener{
		StockInBarcode:  stockIn,
		StockOutBarcode: stockOut,
		HeadlessUserID:  envOrDefault("HEADLESS_USER_ID", "user-1"),
	}, true
}

// loadScanListenerConfigWithSource reads the ScanListener's env-configurable
// settings and returns a listener with Source, DevicePath, and ScanInputSource
// configured, ready to have its Queue, LookupService, and ModePublisher assigned.
// It returns ok=false when the stock-in and stock-out control barcodes are
// identical, when the source is unrecognized, or when SCAN_INPUT has an
// invalid value. Default source is "device"; default device path is "/dev/input/pantry-scanner".
func loadScanListenerConfigWithSource() (*scanlistener.ScanListener, bool) {
	source, ok := scanlistener.ParseSource(envOrDefault("SCAN_INPUT", ""))
	if !ok {
		log.Printf("scan listener: invalid SCAN_INPUT value, defaulting to device")
	}
	if source == scanlistener.SourceStdin {
		log.Printf("scan listener: reading scans from standard input (SCAN_INPUT=stdin)")
	}

	stockIn := envOrDefault("STOCK_IN_CONTROL_BARCODE", "STOCK_IN")
	stockOut := envOrDefault("STOCK_OUT_CONTROL_BARCODE", "STOCK_OUT")
	if stockIn == stockOut {
		log.Printf("scan listener: STOCK_IN_CONTROL_BARCODE and STOCK_OUT_CONTROL_BARCODE must differ, not starting scan listener")
		return nil, false
	}

	listener := scanlistener.New()
	listener.Source = source
	listener.StockInBarcode = stockIn
	listener.StockOutBarcode = stockOut
	listener.HeadlessUserID = envOrDefault("HEADLESS_USER_ID", "user-1")
	listener.DevicePath = envOrDefault("SCANNER_DEVICE", "/dev/input/pantry-scanner")

	log.Printf("scan listener: configured with source=%s device=%s", source, listener.DevicePath)

	return listener, true
}
