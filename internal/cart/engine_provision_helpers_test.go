package cart

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/suggestion"
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
	}
}

// seedShortfall creates a product with one barcode, an inventory item in target
// mode with the given target and zero instances, and a manual shopping-list
// entry for it. The computed quantity is therefore the full target, so the
// entry reaches Provision with Quantity == target.
func (env *provisionEnv) seedShortfall(t *testing.T, userID, productID, name, barcode string, target int) (itemID, entryID string) {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, env.catalog.CreateProduct(ctx, product.Product{ID: productID, Name: name, Category: "Test"}))
	require.NoError(t, env.catalog.UpsertBarcodeMapping(ctx, barcode, productID, "global", ""))

	item, err := env.pantry.GetOrCreateItem(ctx, userID, productID)
	require.NoError(t, err)
	require.NoError(t, env.pantry.UpdateTargetQuantity(ctx, item.ID, target))

	entry, err := env.shoppingList.AddManualItem(ctx, userID, item.ID, target)
	require.NoError(t, err)

	return item.ID, entry.ID
}

// connect writes a connected connection record so a non-none-auth provider
// passes Provision's connection gate.
func (env *provisionEnv) connect(t *testing.T, providerID ProviderID) {
	t.Helper()
	require.NoError(t, env.directory.Write(context.Background(), &connection.Connection{
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
}

func (f *fakeProvider) ID() ProviderID             { return f.id }
func (f *fakeProvider) DisplayName() string        { return "Fake Provider" }
func (f *fakeProvider) Capabilities() Capabilities { return f.caps }

// fakeScript queues the provider's responses: one disposition per Add call, the
// per-line confirmation map for per_line providers, and the identity each
// barcode resolves to.
type fakeScript struct {
	dispositions  []ResultDisposition
	perLine       map[ProductIdentity]bool
	identityByBar map[string]ProductIdentity
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
	return ProvisionResult{
		Disposition: disposition,
		PerLine:     b.script.perLine,
		Status:      200,
	}, nil
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
	}
}

var (
	_ Provider         = (*fakeProvider)(nil)
	_ ServerPush       = (*fakeProvider)(nil)
	_ DerivedIdentity  = (*fakeProvider)(nil)
	_ LookedUpIdentity = (*fakeProvider)(nil)
)
