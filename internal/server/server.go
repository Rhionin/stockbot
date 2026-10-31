// Package server provides HTTP server configuration and route registration.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/brand"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/deployhook"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/push"
	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/scanlistener"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/suggestion"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/devicingest"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/Rhionin/stockbot/internal/telemetry"
	"github.com/Rhionin/stockbot/internal/webui"
)

// defaultStockInBarcode and defaultStockOutBarcode mirror the headless
// listener's env defaults (STOCK_IN_CONTROL_BARCODE / STOCK_OUT_CONTROL_BARCODE
// in cmd/server/main.go), so GET /api/scanner/config reports usable values even
// when NewHandler is called without WithScannerConfig.
const (
	defaultStockInBarcode  = "STOCK_IN"
	defaultStockOutBarcode = "STOCK_OUT"
)

// config holds the optional configuration for NewHandler.
type config struct {
	broadcaster      *events.Broadcaster
	statusFn         func() scanlistener.Status
	scannerConfig    ScannerConfig
	registry         *cart.Registry
	ledger           *cart.Ledger
	providerEnv      ProviderEnv
	retailer         shopping.RetailerDealConfig
	contributor      product.UpstreamContributor
	scannerMode      *scannerMode
	deployHookSecret string
	deployHookPath   string
	syncToken        string
	readDB           *sql.DB
	push             push.Config
	sentryTunnel     SentryTunnelConfig
	streamShutdown   *StreamShutdown
	stage1svc        *auth.Service
	site             app.RuntimeConfig
	gate             *stage1
}

// Option is a functional option for NewHandler.
type Option func(*config)

// WithCartRegistry supplies the grocery-provider registry and fulfillment
// ledger that back the provider and provisioning endpoints. When unset, the
// cart endpoints wire an empty registry and no-op provisioner, so the default
// install exposes no configured provider.
func WithCartRegistry(registry *cart.Registry, ledger *cart.Ledger) Option {
	return func(c *config) {
		c.registry = registry
		c.ledger = ledger
	}
}

// WithProviderEnv supplies deploy-time cart credentials. Saved credentials
// override them until cleared. The secret is never copied into a response.
func WithProviderEnv(env ProviderEnv) Option {
	return func(c *config) {
		c.providerEnv = env
	}
}

// WithBroadcaster supplies an externally owned Broadcaster so the scan
// listener can publish mode changes to the same subscribers the HTTP handlers
// publish to. When unset, NewHandler creates its own, as today.
func WithBroadcaster(b *events.Broadcaster) Option {
	return func(c *config) {
		c.broadcaster = b
	}
}

// WithScannerConfig supplies the reserved control-barcode strings that
// GET /api/scanner/config exposes to the browser, sourced from the same
// env-derived values the headless listener uses. When unset, NewHandler falls
// back to the "STOCK_IN"/"STOCK_OUT" defaults so behavior is unchanged out of
// the box.
func WithScannerConfig(cfg ScannerConfig) Option {
	return func(c *config) {
		c.scannerConfig = cfg
	}
}

// WithRetailerDeals records the store-price gate. An empty API key leaves live
// prices disconnected. Sales noted on the shopping plan stay on the list.
func WithRetailerDeals(apiKey, baseURL string) Option {
	return func(c *config) {
		c.retailer = shopping.RetailerDealConfig{APIKey: apiKey, BaseURL: baseURL}
	}
}

// WithContributor supplies the Product Opener writer used when a person
// explicitly opts in to sharing a product. When unset, sharing is recorded
// locally and no upstream request is made.
func WithContributor(contributor product.UpstreamContributor) Option {
	return func(c *config) {
		c.contributor = contributor
	}
}

// WithScannerMode supplies the process-wide scan direction. The headless
// listener stamps its next barcode with the same value POST /api/scanner/mode
// updates. When unset, NewHandler creates one.
func WithScannerMode(mode *scannerMode) Option {
	return func(c *config) {
		c.scannerMode = mode
	}
}

// WithReadDB uses the WAL read pool for queries that do not write.
// A missing pool keeps those reads on the writer.
func WithReadDB(db *sql.DB) Option {
	return func(c *config) {
		c.readDB = db
	}
}

// WithSyncToken enables GET /api/sync/snapshot for the grocy-sync sidecar.
// An empty token leaves the route looking like it is not there. The token
// cannot call any other route.
func WithSyncToken(token string) Option {
	return func(c *config) {
		c.syncToken = strings.TrimSpace(token)
	}
}

// WithDeployHook enables POST /api/deploy-hook. An empty secret leaves the
// route in place and rejecting requests, so a missing key cannot be used as
// an unsigned deploy. path is the trigger file; empty uses the container default.
func WithDeployHook(secret, path string) Option {
	return func(c *config) {
		c.deployHookSecret = secret
		c.deployHookPath = path
	}
}

// WithStreamShutdown ends GET /api/events when the process stops. main
// registers the same value with http.Server.RegisterOnShutdown.
func WithStreamShutdown(s *StreamShutdown) Option {
	return func(c *config) {
		c.streamShutdown = s
	}
}

// WithSentryTunnel enables POST /api/sentry-tunnel. An empty host or an
// empty project list leaves the route looking like it is not there.
func WithSentryTunnel(cfg SentryTunnelConfig) Option {
	return func(c *config) {
		c.sentryTunnel = cfg
	}
}

// WithPush supplies the VAPID keys used to send scan alerts when no tab is
// open. An unconfigured value leaves the subscribe route explaining that
// background alerts are not set up, and the open tab still notifies itself.
func WithPush(cfg push.Config) Option {
	return func(c *config) {
		c.push = cfg
	}
}

// WithScannerStatus supplies the scan listener's status for GET /health. When
// unset, the health response omits the scanner object entirely, which is what
// every existing test sees.
func WithScannerStatus(fn func() scanlistener.Status) Option {
	return func(c *config) {
		c.statusFn = fn
	}
}

// NewHandler creates and configures the HTTP handler with all application
// routes. It also returns the scan.Queue it constructs internally, already
// wired to the same Broadcaster the handler's routes publish through, so
// callers with a second entry point into scan mutations (e.g. a headless
// scan listener) can reuse it instead of constructing an independent Queue
// that would silently skip event broadcasting.
func NewHandler(
	catalog *product.Catalog,
	lookupService *product.LookupService,
	refresher *product.Refresher,
	db *sql.DB,
	opts ...Option,
) (http.Handler, *scan.Queue) {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build the API mux containing all existing routes
	apiMux, scanQueue, reg := newAPIMux(catalog, lookupService, refresher, db, cfg)

	// Create root mux that composes API routes with web UI
	root := http.NewServeMux()
	root.Handle("/api", apiErrors(apiMux))
	root.Handle("/api/", apiErrors(apiMux))
	root.Handle("/health", apiMux)
	root.Handle("/health/", apiMux)
	// Exact public URLs. Registered before the SPA catch-all so /terms and
	// /privacy are these pages, not the app shell.
	brand.Register(root)
	root.Handle("/", webui.NewHandler())

	var handler http.Handler = root
	var syncToken string
	if cfg != nil {
		syncToken = cfg.syncToken
	}
	readDB := db
	if cfg != nil && cfg.readDB != nil {
		readDB = cfg.readDB
	}
	if cfg != nil && cfg.gate != nil {
		handler = cfg.gate.Middleware(handler)
	} else {
		handler = withPrimaryScope(readDB, handler)
	}
	// After stage-1 auth wiring so a sync token on the wrong route is
	// refused before session checks, while snapshot/event reads set
	// syncAuthKey and bypass the gate above.
	handler = syncGate(syncToken, handler)
	return withSentry(observeHTTP(reg, secureHeaders(handler))), scanQueue
}

// newAPIMux creates the API-only mux with all existing route registrations.
// This extraction allows tests to access the bare API mux for comparison
// while keeping the route definitions in one place.
func newAPIMux(
	catalog *product.Catalog,
	lookupService *product.LookupService,
	refresher *product.Refresher,
	db *sql.DB,
	cfg *config,
) (*http.ServeMux, *scan.Queue, *telemetry.Registry) {
	apiMux := http.NewServeMux()

	reg := telemetry.NewRegistry()
	broadcaster := events.NewBroadcaster()
	if cfg != nil && cfg.broadcaster != nil {
		broadcaster = cfg.broadcaster
	}
	broadcaster.SetCursor(func(householdID, scope string) string {
		cursor, err := changes.Current(context.Background(), db, householdID, scope)
		if err != nil {
			return ""
		}
		return cursor
	})
	// The headless listener publishes on this same broadcaster, so the observer
	// has to be attached to the shared instance rather than a private one.
	broadcaster.SetObserver(publishObserver{reg: reg})
	reg.SetSubscriberCount(broadcaster.SubscriberCount)

	// Resolve the cart registry and ledger from config; default to an empty
	// registry and no-op provisioner when no provider is configured.
	var registry *cart.Registry
	var ledger *cart.Ledger
	if cfg != nil {
		registry = cfg.registry
		ledger = cfg.ledger
	}
	if registry == nil {
		registry = cart.NewRegistry()
	}
	if ledger == nil {
		ledger = cart.NewLedger(db)
	}

	apiMux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		response := `{"status":"ok"}`
		if cfg != nil && cfg.statusFn != nil {
			status := cfg.statusFn()
			scannerJSON, _ := json.Marshal(status)
			response = fmt.Sprintf(`%s,"scanner":%s}`, response[:len(response)-1], scannerJSON)
		}
		if cfg != nil && cfg.stage1svc != nil {
			if code := cfg.stage1svc.SetupHealth(r.Context()); code != "" {
				response = fmt.Sprintf(`%s,"setupCode":%q}`, response[:len(response)-1], code)
			}
		}
		fmt.Fprintln(w, response)
	})

	var streams *StreamShutdown
	if cfg != nil {
		streams = cfg.streamShutdown
	}
	eventsHandler := &EventsHandler{Broadcaster: broadcaster, Telemetry: reg, Shutdown: streams}
	if cfg != nil && cfg.stage1svc != nil {
		eventsHandler.Streams = cfg.stage1svc.Streams()
	}
	apiMux.HandleFunc("GET /api/events", eventsHandler.Handle)
	// Caddy serves this exact path without the household password, beside
	// telemetry, and isPublicPath leaves it open when X-Pantry-Entry is set.
	// An agent compares commit with master to see that a deploy landed.
	apiMux.HandleFunc("GET /api/build", HandleJSON(handleBuildInfo))

	telemetryHandler := &TelemetryHandler{Registry: reg}
	apiMux.HandleFunc("GET /api/telemetry", telemetryHandler.Get)
	apiMux.HandleFunc("POST /api/telemetry/client", telemetryHandler.PostClient)

	// Caddy does not put this path behind the household login. The signature
	// check is what keeps an unsigned request from deploying.
	var deploySecret, deployPath string
	if cfg != nil {
		deploySecret = cfg.deployHookSecret
		deployPath = cfg.deployHookPath
	}
	apiMux.HandleFunc("POST /api/deploy-hook", deployhook.New(deploySecret, deployPath).ServeHTTP)

	// Open before household login so a signed-out page can report a failure.
	// syncGate also lets this path through. Nothing else is exempted with it.
	var tunnelCfg SentryTunnelConfig
	if cfg != nil {
		tunnelCfg = cfg.sentryTunnel
	}
	apiMux.Handle(sentryTunnelPath, newSentryTunnel(tunnelCfg))

	// Scanner mode + config handlers. The mode handler publishes through the
	// same broadcaster GET /api/events uses, so a browser-initiated mode switch
	// reaches every subscriber exactly as a headless control-barcode scan does.
	// Fall back per field so a caller that sets only one control barcode keeps
	// the value it provided instead of reverting both to defaults.
	scannerConfig := ScannerConfig{StockInBarcode: defaultStockInBarcode, StockOutBarcode: defaultStockOutBarcode}
	if cfg != nil {
		if cfg.scannerConfig.StockInBarcode != "" {
			scannerConfig.StockInBarcode = cfg.scannerConfig.StockInBarcode
		}
		if cfg.scannerConfig.StockOutBarcode != "" {
			scannerConfig.StockOutBarcode = cfg.scannerConfig.StockOutBarcode
		}
	}
	// One scannerMode is shared by the mode switch, config reads, scan creation,
	// and (when the process passes it in) the headless listener. A change,
	// including the idle return to scan-out, publishes scanner_mode on the
	// broadcaster those subscribers already use.
	var mode *scannerMode
	if cfg != nil && cfg.scannerMode != nil {
		mode = cfg.scannerMode
	} else {
		mode = newScannerMode(nil)
	}
	primaryID := ""
	if db != nil {
		if scope, err := auth.PrimaryScope(context.Background(), db); err == nil {
			primaryID = scope.HouseholdID()
		}
	}
	if mode.publish == nil {
		mode.mu.Lock()
		mode.publish = func(d scan.ScanDirection) {
			if primaryID != "" {
				broadcaster.PublishScannerModeFor(primaryID, d)
				return
			}
			broadcaster.PublishScannerModeEvent(d)
		}
		mode.mu.Unlock()
	}
	modes := newModeBook(mode, primaryID, db, broadcaster)
	scannerModeHandler := &ScannerModeHandler{Mode: mode, Book: modes}
	scannerConfigHandler := &ScannerConfigHandler{Config: scannerConfig, Mode: mode, Book: modes}
	if cfg != nil {
		scannerConfigHandler.Status = cfg.statusFn
	}
	apiMux.HandleFunc("POST /api/scanner/mode", HandleJSON(scannerModeHandler.Handle))
	apiMux.HandleFunc("GET /api/scans/mode", HandleJSON(scannerModeHandler.Get))
	apiMux.HandleFunc("POST /api/scans/mode", HandleJSON(scannerModeHandler.Handle))
	apiMux.HandleFunc("GET /api/scanner/config", HandleJSON(scannerConfigHandler.Handle))

	contributor := product.UpstreamContributor(product.UnconfiguredContributor{})
	if cfg != nil && cfg.contributor != nil {
		contributor = cfg.contributor
	}

	// Product handlers
	lookupHandler := &LookupHandler{Service: lookupService}
	listHandler := &ListHandler{Catalog: catalog}
	createHandler := &CreateHandler{Catalog: catalog, Contributor: contributor}
	updateHandler := &UpdateHandler{Catalog: catalog, Contributor: contributor}
	overrideHandler := &OverrideCreateHandler{Catalog: catalog}
	refreshHandler := &RefreshHandler{Refresher: refresher, Catalog: catalog}
	settingsGetHandler := &ContributionSettingsGetHandler{Catalog: catalog, Contributor: contributor}
	settingsPutHandler := &ContributionSettingsPutHandler{Catalog: catalog, Contributor: contributor}
	contributionsHandler := &ContributionsListHandler{Catalog: catalog}
	productContributionsHandler := &ProductContributionsHandler{Catalog: catalog}
	productGetHandler := &ProductGetHandler{Catalog: catalog}
	productSearchHandler := &ProductSearchHandler{Catalog: catalog}

	apiMux.HandleFunc("GET /api/products/lookup", HandleJSON(lookupHandler.Handle))
	apiMux.HandleFunc("GET /api/products/search", HandleJSON(productSearchHandler.Handle))
	apiMux.HandleFunc("GET /api/products", HandleJSON(listHandler.Handle))
	apiMux.HandleFunc("GET /api/products/{id}", HandleJSON(productGetHandler.Handle))
	apiMux.HandleFunc("POST /api/products", HandleJSON(createHandler.Handle))
	apiMux.HandleFunc("PUT /api/products/{id}", HandleJSON(updateHandler.Handle))
	apiMux.HandleFunc("POST /api/products/overrides", HandleJSON(overrideHandler.Handle))
	apiMux.HandleFunc("POST /api/products/{id}/refresh", HandleJSON(refreshHandler.Handle))
	apiMux.HandleFunc("GET /api/products/{id}/contributions", HandleJSON(productContributionsHandler.Handle))
	apiMux.HandleFunc("GET /api/contributions", HandleJSON(contributionsHandler.Handle))
	apiMux.HandleFunc("GET /api/settings/contribution", HandleJSON(settingsGetHandler.Handle))
	apiMux.HandleFunc("PUT /api/settings/contribution", HandleJSON(settingsPutHandler.Handle))

	supplySvc := supply.Open(db)

	// Scan queue handlers
	scanQueue := scan.NewQueue(db)
	scanQueue.Broadcaster = broadcaster
	scanQueue.Ledger = providerLedgerReset{ledger: ledger, registry: registry}
	scanQueue.Supply = supplySvc
	scanCreateHandler := &ScanCreateHandler{
		Queue:         scanQueue,
		LookupService: lookupService,
		Events:        broadcaster,
		Mode:          mode,
		Modes:         modes,
	}
	scanListHandler := &ScanListHandler{Queue: scanQueue, DB: db}
	scanHistoryHandler := &ScanHistoryHandler{Queue: scanQueue}
	scanUpdateHandler := &ScanUpdateHandler{Queue: scanQueue}
	scanCommitHandler := &ScanCommitHandler{Queue: scanQueue}
	scanLinkSuggestionsHandler := &ScanLinkSuggestionsHandler{Queue: scanQueue}
	scanBatchCommitHandler := &ScanBatchCommitHandler{Queue: scanQueue}

	apiMux.HandleFunc("POST /api/scans", HandleJSON(scanCreateHandler.Handle))
	apiMux.HandleFunc("GET /api/scans", HandleJSON(scanListHandler.Handle))
	apiMux.HandleFunc("GET /api/scans/history", HandleJSON(scanHistoryHandler.Handle))
	apiMux.HandleFunc("PATCH /api/scans/{id}", HandleJSON(scanUpdateHandler.Handle))
	apiMux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON(scanCommitHandler.Handle))
	apiMux.HandleFunc("GET /api/scans/{id}/link-suggestions", HandleJSON(scanLinkSuggestionsHandler.Handle))
	apiMux.HandleFunc("POST /api/scans/batch-commit", HandleJSON(scanBatchCommitHandler.Handle))

	// Inventory handlers
	pantry := inventory.NewPantry(db)
	pantry.Broadcaster = broadcaster
	scanQueue.Pantry = pantry
	createHandler.Pantry = pantry
	createHandler.Events = broadcaster
	productEnsureItemHandler := &ProductEnsureItemHandler{Catalog: catalog, Pantry: pantry, Events: broadcaster}
	apiMux.HandleFunc("POST /api/products/{id}/item", HandleJSON(productEnsureItemHandler.Handle))
	inventoryListHandler := &InventoryListHandler{Pantry: pantry, DB: db}
	inventoryInstancesListHandler := &InventoryInstancesListHandler{Pantry: pantry}
	inventoryInstanceCreateHandler := &InventoryInstanceCreateHandler{Queue: scanQueue}
	inventoryInstanceDeleteHandler := &InventoryInstanceDeleteHandler{Queue: scanQueue}
	inventoryStockOutHandler := &InventoryStockOutHandler{Queue: scanQueue}
	inventoryWipeHandler := &InventoryWipeHandler{Supply: supplySvc}
	supplySettingsGetHandler := &SupplySettingsGetHandler{Supply: supplySvc}
	supplySettingsPutHandler := &SupplySettingsPutHandler{Supply: supplySvc}
	groups := group.NewGroups(db)
	groups.SetHints(broadcaster)
	scanListHandler.Groups = groups
	createHandler.Groups = groups
	updateHandler.Groups = groups
	lookupHandler.Groups = groups
	restockTargets := restock.NewTargets(db)
	supplyOverrideGetHandler := &SupplyOverrideGetHandler{Targets: restockTargets, Groups: groups}
	supplyOverridePutHandler := &SupplyOverridePutHandler{Targets: restockTargets, Groups: groups}
	onboardingCompleteHandler := &OnboardingCompleteHandler{Supply: supplySvc}

	apiMux.HandleFunc("GET /api/inventory", HandleJSON(inventoryListHandler.Handle))
	apiMux.HandleFunc("GET /api/inventory/{itemId}/instances", HandleJSON(inventoryInstancesListHandler.Handle))
	apiMux.HandleFunc("POST /api/inventory/{itemId}/instances", HandleJSON(inventoryInstanceCreateHandler.Handle))
	apiMux.HandleFunc("POST /api/inventory/{itemId}/stock-out", HandleJSON(inventoryStockOutHandler.Handle))
	apiMux.HandleFunc("DELETE /api/inventory/instances/{instanceId}", HandleJSON(inventoryInstanceDeleteHandler.Handle))
	apiMux.HandleFunc("POST /api/inventory/wipe", HandleJSON(inventoryWipeHandler.Handle))

	var syncToken string
	readDB := db
	if cfg != nil {
		syncToken = cfg.syncToken
		if cfg.readDB != nil {
			readDB = cfg.readDB
		}
	}
	apiMux.HandleFunc("GET /api/sync/snapshot", (&syncSnapshotHandler{DB: readDB, Token: syncToken}).ServeHTTP)

	historyHandler := &HistoryHandler{Journal: history.NewJournal(db), Pantry: pantry, Events: broadcaster}
	apiMux.HandleFunc("GET /api/history", HandleJSON(historyHandler.Household))
	apiMux.HandleFunc("GET /api/items/{itemId}/history", HandleJSON(historyHandler.Product))
	apiMux.HandleFunc("GET /api/groups/{groupId}/history", HandleJSON(historyHandler.Group))
	apiMux.HandleFunc("PATCH /api/moves/{id}", HandleJSON(historyHandler.SetQuantity))
	apiMux.HandleFunc("GET /api/moves/{id}/preview", HandleJSON(historyHandler.PreviewUndo))
	apiMux.HandleFunc("DELETE /api/moves/{id}", HandleJSON(historyHandler.Undo))

	apiMux.HandleFunc("GET /api/settings/supply", HandleJSON(supplySettingsGetHandler.Handle))
	apiMux.HandleFunc("PUT /api/settings/supply", HandleJSON(supplySettingsPutHandler.Handle))
	apiMux.HandleFunc("GET /api/products/{id}/supply-override", HandleJSON(supplyOverrideGetHandler.Handle))
	apiMux.HandleFunc("PUT /api/products/{id}/supply-override", HandleJSON(supplyOverridePutHandler.Handle))
	apiMux.HandleFunc("POST /api/onboarding/complete", HandleJSON(onboardingCompleteHandler.Handle))

	restockHandler := &RestockHandler{Targets: restockTargets}
	apiMux.HandleFunc("GET /api/restock", HandleJSON(restockHandler.Snapshot))
	apiMux.HandleFunc("PUT /api/restock/household", HandleJSON(restockHandler.PutHousehold))
	apiMux.HandleFunc("PUT /api/restock/items/{itemId}", HandleJSON(restockHandler.PutItem))
	apiMux.HandleFunc("DELETE /api/restock/items/{itemId}", HandleJSON(restockHandler.DeleteItem))
	apiMux.HandleFunc("POST /api/restock/items/{itemId}/dont-restock", HandleJSON(restockHandler.DontRestock))
	apiMux.HandleFunc("POST /api/restock/items/{itemId}/categories", HandleJSON(restockHandler.AddItemCategory))
	apiMux.HandleFunc("DELETE /api/restock/items/{itemId}/categories/{categoryId}", HandleJSON(restockHandler.RemoveItemCategory))
	apiMux.HandleFunc("GET /api/restock/categories", HandleJSON(restockHandler.Categories))
	apiMux.HandleFunc("POST /api/restock/categories", HandleJSON(restockHandler.CreateCategory))
	apiMux.HandleFunc("PUT /api/restock/categories/{id}/target", HandleJSON(restockHandler.PutCategoryTarget))
	apiMux.HandleFunc("POST /api/restock/categories/{id}/preview", HandleJSON(restockHandler.PreviewCategory))
	apiMux.HandleFunc("PATCH /api/restock/categories/{id}", HandleJSON(restockHandler.RenameCategory))
	apiMux.HandleFunc("POST /api/restock/categories/{id}/merge", HandleJSON(restockHandler.MergeCategory))
	apiMux.HandleFunc("DELETE /api/restock/categories/{id}", HandleJSON(restockHandler.DeleteCategory))
	apiMux.HandleFunc("POST /api/restock/groups/{id}/preview", HandleJSON(restockHandler.PreviewGroup))
	apiMux.HandleFunc("GET /api/restock/suggestions", HandleJSON(restockHandler.Suggestions))
	apiMux.HandleFunc("POST /api/restock/suggestions/apply", HandleJSON(restockHandler.ApplySuggestions))
	apiMux.HandleFunc("POST /api/restock/suggestions/dismiss", HandleJSON(restockHandler.DismissSuggestions))

	groupHandler := &GroupHandler{Groups: groups, Supply: supplySvc}
	apiMux.HandleFunc("GET /api/groups", HandleJSON(groupHandler.List))
	apiMux.HandleFunc("POST /api/groups", HandleJSON(groupHandler.Create))
	apiMux.HandleFunc("POST /api/groups/preview", HandleJSON(groupHandler.Preview))
	apiMux.HandleFunc("GET /api/groups/{id}", HandleJSON(groupHandler.Get))
	apiMux.HandleFunc("PATCH /api/groups/{id}", HandleJSON(groupHandler.Patch))
	apiMux.HandleFunc("DELETE /api/groups/{id}", HandleJSON(groupHandler.Delete))
	apiMux.HandleFunc("POST /api/groups/{id}/members", HandleJSON(groupHandler.AddMembers))
	apiMux.HandleFunc("DELETE /api/groups/{id}/members/{productId}", HandleJSON(groupHandler.RemoveMember))
	apiMux.HandleFunc("PUT /api/groups/{id}/members/{productId}/restock", HandleJSON(groupHandler.PutMemberRestock))
	apiMux.HandleFunc("PUT /api/groups/{id}/rule", HandleJSON(groupHandler.PutRule))
	apiMux.HandleFunc("PUT /api/groups/{id}/target", HandleJSON(groupHandler.PutTarget))
	apiMux.HandleFunc("GET /api/group-suggestions", HandleJSON(groupHandler.ListSuggestions))
	apiMux.HandleFunc("POST /api/group-suggestions/rescan", HandleJSON(groupHandler.Rescan))
	apiMux.HandleFunc("POST /api/group-suggestions/from-scan", HandleJSON(groupHandler.NoteFromScan))
	apiMux.HandleFunc("POST /api/group-suggestions/{id}/accept", HandleJSON(groupHandler.AcceptSuggestion))
	apiMux.HandleFunc("POST /api/group-suggestions/{id}/dismiss", HandleJSON(groupHandler.DismissSuggestion))
	apiMux.HandleFunc("POST /api/group-suggestions/{id}/skip", HandleJSON(groupHandler.SkipSuggestion))
	apiMux.HandleFunc("GET /api/settings/group-rule", HandleJSON(groupHandler.GetDefaultRule))
	apiMux.HandleFunc("PUT /api/settings/group-rule", HandleJSON(groupHandler.PutDefaultRule))

	// Target quantity remains a stored field. It does not decide a shopping line.
	consumptionLog := suggestion.NewConsumptionLog(db)
	setTargetQuantityHandler := &SetTargetQuantityHandler{
		Pantry: pantry,
	}
	apiMux.HandleFunc("POST /api/items/{itemId}/target-quantity", HandleJSON(setTargetQuantityHandler.Handle))

	// Shopping list handlers
	shoppingList := shopping.NewStore(db)
	shoppingList.SetHints(broadcaster)
	connDir := connection.NewDirectory(db)
	credentialVault := appcred.NewVault(db)
	tokenBroker := connection.NewTokenBroker(connDir)
	engine := cart.NewEngine(registry, ledger, tokenBroker)
	engine.SetShoppingList(*shoppingList)
	engine.SetPantry(*pantry)
	engine.SetConsumptionLog(*consumptionLog)
	if catalog != nil {
		engine.SetCatalog(*catalog)
	}

	shoppingListGetHandler := &ShoppingListGetHandler{
		ShoppingList: shoppingList,
		Pantry:       pantry,
		Ledger:       ledger,
		Registry:     registry,
		Adjustments:  shoppingList,
		DB:           db,
	}
	shoppingListFillHandler := &ShoppingListFillHandler{
		ShoppingList: shoppingList,
		Pantry:       pantry,
		Supply:       supplySvc,
		Ledger:       ledger,
		Registry:     registry,
		Adjustments:  shoppingList,
	}
	refreshPlan := func(ctx context.Context) error {
		scope, err := requestScope(ctx)
		if err != nil {
			scope, err = auth.PrimaryScope(ctx, db)
			if err != nil {
				return err
			}
		}
		_, err = stageShoppingPlan(ctx, scope, pantry, shoppingList, supplySvc, time.Now())
		return err
	}
	restockHandler.Refresh = refreshPlan
	groupHandler.Refresh = refreshPlan
	shoppingListItemCreateHandler := &ShoppingListItemCreateHandler{
		ShoppingList: shoppingList,
		Pantry:       pantry,
	}
	shoppingListItemDeleteHandler := &ShoppingListItemDeleteHandler{
		ShoppingList: shoppingList,
	}
	shoppingListItemUpdateHandler := &ShoppingListItemUpdateHandler{
		ShoppingList: shoppingList,
	}
	shoppingListExportHandler := &ShoppingListExportHandler{
		ShoppingList: shoppingList,
		Pantry:       pantry,
		Provisioner:  engine,
		Ledger:       ledger,
		Registry:     registry,
		Connections:  connDir,
	}
	shoppingDealPutHandler := &ShoppingDealPutHandler{
		ShoppingList: shoppingList,
		Pantry:       pantry,
	}
	shoppingDealDeleteHandler := &ShoppingDealDeleteHandler{
		ShoppingList: shoppingList,
	}

	apiMux.HandleFunc("GET /api/shopping-list", HandleJSON(shoppingListGetHandler.Handle))
	apiMux.HandleFunc("POST /api/shopping-list/fill", HandleJSON(shoppingListFillHandler.Handle))
	apiMux.HandleFunc("POST /api/shopping-list/items", HandleJSON(shoppingListItemCreateHandler.Handle))
	apiMux.HandleFunc("DELETE /api/shopping-list/items/{id}", HandleJSON(shoppingListItemDeleteHandler.Handle))
	apiMux.HandleFunc("PATCH /api/shopping-list/items/{id}", HandleJSON(shoppingListItemUpdateHandler.Handle))
	apiMux.HandleFunc("POST /api/shopping-list/items/{id}/swap", HandleJSON((&ShoppingListSwapHandler{ShoppingList: shoppingList}).Handle))
	apiMux.HandleFunc("PUT /api/shopping-list/deals", HandleJSON(shoppingDealPutHandler.Handle))
	apiMux.HandleFunc("DELETE /api/shopping-list/deals/{itemId}", HandleJSON(shoppingDealDeleteHandler.Handle))
	apiMux.HandleFunc("POST /api/shopping-list/export", HandleJSON(shoppingListExportHandler.Handle))

	// Cart integration handlers. connDir is created with the shopping list
	// above so the provisioner and these routes share one directory.
	var envFallback ProviderEnv
	if cfg != nil {
		envFallback = cfg.providerEnv
	}
	providersHandler := &ProvidersHandler{
		Registry:      registry,
		Adapters:      cart.ServerRegistryResolver{Registry: registry},
		ConnectionDir: connDir,
		Credentials:   credentialVault,
		EnvFallback:   envFallback,
	}
	if cfg != nil && cfg.stage1svc != nil {
		providersHandler.Auth = cfg.stage1svc
	}

	listProvidersHandler := &ListProvidersHandler{ProvidersHandler: *providersHandler}
	providerAuthorizeHandler := &ProviderAuthorizeHandler{ProvidersHandler: *providersHandler}
	providerCallbackHandler := &ProviderCallbackHandler{ProvidersHandler: *providersHandler}
	providerDisconnectHandler := &ProviderDisconnectHandler{ProvidersHandler: *providersHandler}
	providerLedgerHandler := &ProviderLedgerHandler{
		ProvidersHandler: *providersHandler,
		Ledger:           ledger,
	}
	providerLedgerGetHandler := &ProviderLedgerGetHandler{ProviderLedgerHandler: *providerLedgerHandler}
	providerLedgerResetHandler := &ProviderLedgerResetHandler{
		ProviderLedgerHandler: *providerLedgerHandler,
		ShoppingList:          shoppingList,
	}

	apiMux.HandleFunc("GET /api/providers", HandleJSON(listProvidersHandler.Handle))
	apiMux.HandleFunc("GET /api/providers/{providerId}/authorize", HandleJSON(providerAuthorizeHandler.Handle))
	apiMux.HandleFunc("GET /api/providers/{providerId}/callback", HandleJSON(providerCallbackHandler.Handle))
	apiMux.HandleFunc("DELETE /api/providers/{providerId}/connection", HandleJSON(providerDisconnectHandler.Handle))
	apiMux.HandleFunc("PUT /api/providers/{providerId}/credentials", HandleJSON((&ProviderCredentialPutHandler{ProvidersHandler: *providersHandler}).Handle))
	apiMux.HandleFunc("DELETE /api/providers/{providerId}/credentials", HandleJSON((&ProviderCredentialDeleteHandler{ProvidersHandler: *providersHandler}).Handle))
	apiMux.HandleFunc("GET /api/providers/{providerId}/ledger", HandleJSON(providerLedgerGetHandler.Handle))
	apiMux.HandleFunc("POST /api/providers/{providerId}/ledger/reset", HandleJSON(providerLedgerResetHandler.Handle))

	// Replenishment mode handler
	setReplenishmentModeHandler := &SetReplenishmentModeHandler{
		Pantry: pantry,
	}
	apiMux.HandleFunc("POST /api/items/{itemId}/replenishment-mode", HandleJSON(setReplenishmentModeHandler.Handle))

	// Adjustment handler
	shoppingListAdjustmentHandler := &ShoppingListAdjustmentHandler{
		ShoppingList: shoppingList,
	}
	apiMux.HandleFunc("PUT /api/shopping-list/items/{id}/adjustment", HandleJSON(shoppingListAdjustmentHandler.Handle))
	apiMux.HandleFunc("DELETE /api/shopping-list/items/{id}/adjustment", HandleJSON(shoppingListAdjustmentHandler.Handle))

	var pushCfg push.Config
	if cfg != nil {
		pushCfg = cfg.push
	}
	pushSubscriptions := push.NewSubscriptions(db)
	pushHandler := &PushHandler{Subscriptions: pushSubscriptions, Keys: pushCfg}
	apiMux.HandleFunc("GET /api/push/vapid-public-key", HandleJSON(pushHandler.PublicKey))
	apiMux.HandleFunc("POST /api/push/subscriptions", HandleJSON(pushHandler.Save))
	apiMux.HandleFunc("DELETE /api/push/subscriptions", HandleJSON(pushHandler.Delete))
	if cfg != nil && cfg.stage1svc != nil {
		cfg.gate = &stage1{
			svc:      cfg.stage1svc,
			site:     cfg.site,
			stockIn:  scannerConfig.StockInBarcode,
			stockOut: scannerConfig.StockOutBarcode,
		}
		cfg.stage1svc.SetHints(broadcaster)
		cfg.stage1svc.SetLive(broadcaster)
		sender := push.NewSender(pushSubscriptions, pushCfg)
		cfg.stage1svc.SetOperatorPush(func(ctx context.Context, note auth.OperatorNote) {
			sender.NotifyOperators(ctx, push.Notice{
				Title: note.Title,
				Body:  note.Body,
				Tag:   "operator-" + note.Kind,
				URL:   "/",
			})
		})
		devicingest.Attach(cfg.stage1svc, scanQueue, lookupService)
	}
	registerStage1(apiMux, cfg)
	// The listener uses the broadcaster this mux already publishes scans on,
	// including scans from the headless reader when it shares that broadcaster.
	if db != nil {
		if primary, err := auth.PrimaryScope(context.Background(), db); err == nil {
			push.Listen(context.Background(), primary, broadcaster, push.NewSender(pushSubscriptions, pushCfg), scanQueue)
		}
	}

	// Unknown resolution handler
	unknownResolutionHandler := &UnknownResolutionHandler{
		ShoppingList: shoppingList,
		Ledger:       ledger,
	}
	apiMux.HandleFunc("POST /api/shopping-list/items/{id}/unknown-resolution", HandleJSON(unknownResolutionHandler.Handle))

	return apiMux, scanQueue, reg
}

type deviceProductResolver struct {
	lookup *product.LookupService
}

func (r deviceProductResolver) Lookup(ctx context.Context, scope tenant.Scope, barcode string) (auth.DeviceProductMatch, error) {
	res, err := r.lookup.Lookup(ctx, scope, barcode)
	if err != nil {
		return auth.DeviceProductMatch{}, err
	}
	if !res.IsFound() {
		return auth.DeviceProductMatch{}, nil
	}
	return auth.DeviceProductMatch{
		Found:     true,
		ProductID: res.Product.ID,
		Name:      res.Product.Name,
	}, nil
}
