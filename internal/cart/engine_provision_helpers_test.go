package cart

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/suggestion"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// provisionEnv wires every Engine collaborator against one shared in-memory
// database, the arrangement Provision reads through end to end.
type provisionEnv struct {
	db           *sql.DB
	engine       *Engine
	registry     *Registry
	ledger       *Ledger
	directory    *connection.Directory
	catalog      *product.Catalog
	pantry       *inventory.Pantry
	shoppingList *shopping.Store
	scope        tenant.Scope
}

func newProvisionEnv(t *testing.T) *provisionEnv {
	t.Helper()

	db, err := app.OpenSQLite(":memory:")
	require.NoError(t, err)
	// A :memory: database lives inside one connection, and Provision opens a
	// transaction on the same handle it queries through. Pin the pool to one
	// connection so every statement sees the migrated schema, as production
	// does in cmd/server/main.go.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, app.RunMigrations(db))
	scope, err := auth.PrimaryScope(context.Background(), db)
	require.NoError(t, err)

	registry := NewRegistry()
	ledger := NewLedger(db)
	directory := connection.NewDirectory(db)
	tokenBroker := connection.NewTokenBroker(directory)

	engine := NewEngine(registry, ledger, tokenBroker)
	engine.SetShoppingList(*shopping.NewStore(db))
	engine.SetPantry(*inventory.NewPantry(db))
	engine.SetConsumptionLog(*suggestion.NewConsumptionLog(db))
	engine.SetCatalog(*product.NewCatalog(db))

	return &provisionEnv{
		db:           db,
		engine:       engine,
		registry:     registry,
		ledger:       ledger,
		directory:    directory,
		catalog:      product.NewCatalog(db),
		pantry:       inventory.NewPantry(db),
		shoppingList: shopping.NewStore(db),
		scope:        scope,
	}
}

// seedShortfall creates a product with one barcode, an inventory item in target
// mode with the given target and zero instances, and a manual shopping-list
// entry for it. The computed quantity is therefore the full target, so the
// entry reaches Provision with Quantity == target.
func (env *provisionEnv) seedShortfall(t *testing.T, productID, name, barcode string, target int) (itemID, entryID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.catalog.CreateProduct(ctx, product.Product{ID: productID, Name: name, Category: "Test"}))
	require.NoError(t, env.catalog.UpsertBarcodeMapping(ctx, barcode, productID, "global", ""))

	item, err := env.pantry.GetOrCreateItem(ctx, env.scope, productID)
	require.NoError(t, err)
	require.NoError(t, env.pantry.UpdateTargetQuantity(ctx, env.scope, item.ID, target))

	entry, err := env.shoppingList.AddManualItem(ctx, env.scope, item.ID, target)
	require.NoError(t, err)

	return item.ID, entry.ID
}

// connect writes a connected connection record so a non-none-auth provider
// passes Provision's connection gate.
func (env *provisionEnv) connect(t *testing.T, providerID ProviderID) {
	t.Helper()
	require.NoError(t, env.directory.Write(context.Background(), env.scope, &connection.Connection{
		Provider: string(providerID),
		State:    connection.StateConnected,
	}))
}

// fakeProvider is a capability-declaring test provider. It embeds pointers to
// the behavior mixins its capabilities require and no others, so its method set
// satisfies exactly the corresponding narrow cart interfaces (the same shape
// Registry and Engine assert against at runtime).
type fakeProvider struct {
	id   ProviderID
	caps Capabilities
	*serverPushBehavior
	*derivedIdentityBehavior
	*lookedUpIdentityBehavior
	*oauthFlowBehavior
}

func (f *fakeProvider) ID() ProviderID             { return f.id }
func (f *fakeProvider) DisplayName() string        { return "Fake Provider" }
func (f *fakeProvider) Capabilities() Capabilities { return f.caps }

// fakeScript queues the provider's responses: one disposition per Add call, the
// per-line confirmation map for per_line providers, and the identity each
// barcode resolves to.
type fakeScript struct {
	dispositions  []ResultDisposition
	addStatuses   []int
	addErrors     []error
	perLine       map[ProductIdentity]bool
	identityByBar map[string]ProductIdentity
	refreshErr    error
	addBlock      chan struct{} // when non-nil, Add blocks until it is closed
	addStarted    chan struct{} // closed by Add once it is running
}

type serverPushBehavior struct{ script *fakeScript }

func (b *serverPushBehavior) Add(ctx context.Context, cred Credential, req ProvisionRequest) (ProvisionResult, error) {
	if b.script.addStarted != nil {
		select {
		case <-b.script.addStarted:
		default:
			close(b.script.addStarted)
		}
	}
	if b.script.addBlock != nil {
		<-b.script.addBlock
	}

	disposition := DispositionAccepted
	if len(b.script.dispositions) > 0 {
		disposition = b.script.dispositions[0]
		b.script.dispositions = b.script.dispositions[1:]
	}
	status := http.StatusOK
	if len(b.script.addStatuses) > 0 {
		status = b.script.addStatuses[0]
		b.script.addStatuses = b.script.addStatuses[1:]
	}
	var err error
	if len(b.script.addErrors) > 0 {
		err = b.script.addErrors[0]
		b.script.addErrors = b.script.addErrors[1:]
	}
	return ProvisionResult{
		Disposition: disposition,
		PerLine:     b.script.perLine,
		Status:      status,
	}, err
}

type derivedIdentityBehavior struct{ script *fakeScript }

func (b *derivedIdentityBehavior) DeriveIdentity(barcode string) (ProductIdentity, bool) {
	id, ok := b.script.identityByBar[barcode]
	return id, ok
}

type lookedUpIdentityBehavior struct{ script *fakeScript }

func (b *lookedUpIdentityBehavior) LookUpIdentity(ctx context.Context, cred Credential, barcode string) (ProductIdentity, bool, error) {
	id, ok := b.script.identityByBar[barcode]
	return id, ok, nil
}

type oauthFlowBehavior struct{ script *fakeScript }

func (o *oauthFlowBehavior) AuthorizationScope() string { return "cart.test" }

func (o *oauthFlowBehavior) AuthorizationURL(state string) (string, error) {
	return "https://example.test/oauth?" + state, nil
}

func (o *oauthFlowBehavior) ExchangeCode(context.Context, string) (TokenSet, error) {
	return TokenSet{}, nil
}

func (o *oauthFlowBehavior) RefreshAccessToken(context.Context, string) (TokenSet, error) {
	if o.script.refreshErr != nil {
		return TokenSet{}, o.script.refreshErr
	}
	return TokenSet{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: time.Hour}, nil
}

// newFakeProvider builds a server_push provider for caps backed by script.
// Both identity behaviors are attached; Provision calls only the one caps
// selects, so the unused method never runs.
func newFakeProvider(id ProviderID, caps Capabilities, script *fakeScript) *fakeProvider {
	return &fakeProvider{
		id:                       id,
		caps:                     caps,
		serverPushBehavior:       &serverPushBehavior{script: script},
		derivedIdentityBehavior:  &derivedIdentityBehavior{script: script},
		lookedUpIdentityBehavior: &lookedUpIdentityBehavior{script: script},
		oauthFlowBehavior:        &oauthFlowBehavior{script: script},
	}
}

var (
	_ Provider         = (*fakeProvider)(nil)
	_ ServerPush       = (*fakeProvider)(nil)
	_ DerivedIdentity  = (*fakeProvider)(nil)
	_ LookedUpIdentity = (*fakeProvider)(nil)
	_ OAuthFlow        = (*fakeProvider)(nil)
)
