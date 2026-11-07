package cart_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/suggestion"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestProvisionKrogerHouseholdModality_EndToEnd exercises cart.Engine.Provision → Kroger
// cart.ServerPush.Add with httptest (no api.kroger.com) and household modality from
// appcred.Vault.
func TestProvisionKrogerHouseholdModality_EndToEnd(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		adapterMod   string
		setupVault   func(t *testing.T, env *krogerProvisionEnv, vault *appcred.Vault)
		wantModality string
	}{
		{
			name:       "household delivery with server pickup default",
			adapterMod: "PICKUP",
			setupVault: func(t *testing.T, env *krogerProvisionEnv, vault *appcred.Vault) {
				t.Helper()
				require.NoError(t, vault.Save(ctx, env.scope, appcred.Saved{
					ProviderID:   "kroger",
					ClientID:     "hh-app",
					ClientSecret: "secret",
					RedirectURI:  "https://app/cb",
					Modality:     "DELIVERY",
					ActorUserID:  "user-1",
				}))
			},
			wantModality: "DELIVERY",
		},
		{
			name:       "household pickup with server delivery default",
			adapterMod: "DELIVERY",
			setupVault: func(t *testing.T, env *krogerProvisionEnv, vault *appcred.Vault) {
				t.Helper()
				require.NoError(t, vault.Save(ctx, env.scope, appcred.Saved{
					ProviderID:   "kroger",
					ClientID:     "hh-app",
					ClientSecret: "secret",
					RedirectURI:  "https://app/cb",
					Modality:     "PICKUP",
					ActorUserID:  "user-1",
				}))
			},
			wantModality: "PICKUP",
		},
		{
			name:         "no household modality falls back to adapter default",
			adapterMod:   "DELIVERY",
			setupVault:   func(t *testing.T, env *krogerProvisionEnv, vault *appcred.Vault) { t.Helper() },
			wantModality: "DELIVERY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/v1/cart/add" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				b, err := io.ReadAll(io.LimitReader(r.Body, 65536))
				if err != nil {
					t.Errorf("read body: %v", err)
					http.Error(w, "bad body", http.StatusBadRequest)
					return
				}
				gotBody = string(b)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)

			env := newKrogerProvisionEnv(t)
			vault := appcred.NewVault(env.db)
			env.engine.SetCredentialVault(vault)
			tt.setupVault(t, env, vault)

			adapter, err := kroger.New("client", "secret", "https://example/cb", tt.adapterMod)
			require.NoError(t, err)
			adapter.WithBaseURL(srv.URL).WithTransport(srv.Client().Transport)
			require.NoError(t, env.registry.Register(adapter))

			expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
			require.NoError(t, env.directory.Write(ctx, env.scope, &connection.Connection{
				Provider:     "kroger",
				State:        connection.StateConnected,
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				ExpiresAt:    &expires,
			}))

			seedShortfall(t, env, "prod-1", "Milk", "011110728227", 1)

			_, err = env.engine.Provision(ctx, env.scope, "kroger")
			require.NoError(t, err)
			require.NotEmpty(t, gotBody)

			var payload struct {
				Modality string `json:"modality"`
				Items    []struct {
					Modality string `json:"modality"`
				} `json:"items"`
			}
			require.NoError(t, json.Unmarshal([]byte(gotBody), &payload))
			require.Equal(t, tt.wantModality, payload.Modality, "json body: %s", gotBody)
			require.Len(t, payload.Items, 1)
			require.Equal(t, tt.wantModality, payload.Items[0].Modality, "json body: %s", gotBody)
		})
	}
}

func TestProvisionKrogerModality_DoesNotLeakFromOtherHouseholdVault(t *testing.T) {
	ctx := context.Background()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/v1/cart/add" {
			b, err := io.ReadAll(io.LimitReader(r.Body, 65536))
			if err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			gotBody = string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	env := newKrogerProvisionEnv(t)
	vault := appcred.NewVault(env.db)
	env.engine.SetCredentialVault(vault)

	otherHouse := "01909999-9999-7000-8000-000000000099"
	_, err := env.db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Other', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, otherHouse)
	require.NoError(t, err)
	otherScope, err := tenant.New(otherHouse)
	require.NoError(t, err)

	require.NoError(t, vault.Save(ctx, otherScope, appcred.Saved{
		ProviderID:   "kroger",
		ClientID:     "other-app",
		ClientSecret: "other-secret",
		RedirectURI:  "https://other/cb",
		Modality:     "DELIVERY",
		ActorUserID:  "user-2",
	}))

	adapter, err := kroger.New("client", "secret", "https://example/cb", "PICKUP")
	require.NoError(t, err)
	adapter.WithBaseURL(srv.URL).WithTransport(srv.Client().Transport)
	require.NoError(t, env.registry.Register(adapter))

	expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, env.directory.Write(ctx, env.scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    &expires,
	}))

	seedShortfall(t, env, "prod-1", "Milk", "011110728227", 1)

	_, err = env.engine.Provision(ctx, env.scope, "kroger")
	require.NoError(t, err)
	require.NotEmpty(t, gotBody)

	var payload struct {
		Modality string `json:"modality"`
		Items    []struct {
			Modality string `json:"modality"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(gotBody), &payload))
	require.Equal(t, "PICKUP", payload.Modality, "primary household must use adapter default, not other household vault; body=%s", gotBody)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "PICKUP", payload.Items[0].Modality, "body=%s", gotBody)
}

type krogerProvisionEnv struct {
	db           *sql.DB
	engine       *cart.Engine
	registry     *cart.Registry
	ledger       *cart.Ledger
	directory    *connection.Directory
	catalog      *product.Catalog
	pantry       *inventory.Pantry
	shoppingList *shopping.Store
	scope        tenant.Scope
}

func newKrogerProvisionEnv(t *testing.T) *krogerProvisionEnv {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, app.RunMigrations(db))
	scope, err := auth.PrimaryScope(context.Background(), db)
	require.NoError(t, err)

	registry := cart.NewRegistry()
	ledger := cart.NewLedger(db)
	directory := connection.NewDirectory(db)
	tokenBroker := connection.NewTokenBroker(directory)

	engine := cart.NewEngine(registry, ledger, tokenBroker)
	engine.SetShoppingList(*shopping.NewStore(db))
	engine.SetPantry(*inventory.NewPantry(db))
	engine.SetConsumptionLog(*suggestion.NewConsumptionLog(db))
	engine.SetCatalog(*product.NewCatalog(db))

	return &krogerProvisionEnv{
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

func seedShortfall(t *testing.T, env *krogerProvisionEnv, productID, name, barcode string, target int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.catalog.CreateProduct(ctx, product.Product{ID: productID, Name: name, Category: "Test"}))
	require.NoError(t, env.catalog.UpsertBarcodeMapping(ctx, barcode, productID, "global", ""))

	item, err := env.pantry.GetOrCreateItem(ctx, env.scope, productID)
	require.NoError(t, err)
	require.NoError(t, env.pantry.UpdateTargetQuantity(ctx, env.scope, item.ID, target))

	_, err = env.shoppingList.AddManualItem(ctx, env.scope, item.ID, target)
	require.NoError(t, err)
}
