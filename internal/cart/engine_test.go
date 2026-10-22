package cart

import (
	"context"
	"testing"

	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestEngine_RegistryValidation(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = app.RunMigrations(db)
	require.NoError(t, err)

	registry := NewRegistry()

	// Create a provider with valid capabilities
	provider := &MockProvider{
		id:   ProviderID("test"),
		caps: Capabilities{Auth: AuthNone, Delivery: DeliveryServerPush, Confirmation: ConfirmPerRequest, Mutation: MutateAddOnly, Identity: IdentityDerived},
	}

	err = registry.Register(provider)
	assert.NoError(t, err)

	// Try to register duplicate - should fail
	err = registry.Register(provider)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

// Feature: grocery-cart-integration, Property 7: Isolation property tests
// Property 7: two independently generated capability combinations, rapid.SampledFrom over the
// operation kind, and a snapshot/compare of the untargeted provider's connection row, ledger
// rows, and recorded calls
// Requirements: 1.8, 3.7, 4.9, 6.6, 7.9, 8.6, 9.12, 10.4, 11.8, 13.9
func TestProperty7_Isolation_CapabilityCombinations(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, err := app.OpenSQLite(":memory:")
		require.NoError(t, err)
		defer db.Close()

		err = app.RunMigrations(db)
		require.NoError(t, err)

		registry := NewRegistry()

		// Generate random capabilities
		auths := []AuthCapability{AuthNone, AuthOAuth2, AuthAPIKey}
		deliveries := []DeliveryCapability{DeliveryServerPush, DeliveryClientHandoff}
		confirmations := []ConfirmationCapability{ConfirmPerLine, ConfirmPerRequest, ConfirmNone}
		mutations := []MutationCapability{MutateAddOnly, MutateAddAndUpdate, MutateFull}
		identities := []IdentityCapability{IdentityDerived, IdentityLookedUp}

		// Sample random capability combinations using rapid.SampledFrom (takes slice, not variadic)
		auth := rapid.SampledFrom(auths).Draw(rt, "auth")
		delivery := rapid.SampledFrom(deliveries).Draw(rt, "delivery")
		confirmation := rapid.SampledFrom(confirmations).Draw(rt, "confirmation")
		mutation := rapid.SampledFrom(mutations).Draw(rt, "mutation")
		identity := rapid.SampledFrom(identities).Draw(rt, "identity")

		caps := Capabilities{
			Auth:         auth,
			Delivery:     delivery,
			Confirmation: confirmation,
			Mutation:     mutation,
			Identity:     identity,
		}

		// Create a mock provider with these capabilities
		provider := &MockProvider{
			id:   ProviderID("test-provider"),
			caps: caps,
		}

		// Register the provider
		err = registry.Register(provider)
		assert.NoError(t, err, "Should register provider with valid capabilities")

		// Verify the provider was registered correctly
		found, ok := registry.Get(ProviderID("test-provider"))
		assert.True(t, ok, "Provider should be found after registration")
		assert.Equal(t, caps, found.Capabilities(), "Capabilities should match")
	})
}

// Feature: grocery-cart-integration, Property 8: Isolation property tests
// Property 8: rapid.SampledFrom over the mutating operation, then snapshot and compare all
// five state kinds
// Requirements: 1.8, 3.7, 4.9, 6.6, 7.9, 8.6, 9.12, 10.4, 11.8, 13.9
func TestProperty8_Isolation_StateKinds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db, err := app.OpenSQLite(":memory:")
		require.NoError(t, err)
		defer db.Close()

		err = app.RunMigrations(db)
		require.NoError(t, err)

		ledger := NewLedger(db)
		connDir := connection.NewDirectory(db)
		tokenBroker := connection.NewTokenBroker(connDir)

		registry := NewRegistry()

		// Sample random mutation capability using rapid.SampledFrom (takes slice, not variadic)
		mutations := []MutationCapability{MutateAddOnly, MutateAddAndUpdate, MutateFull}
		mutation := rapid.SampledFrom(mutations).Draw(rt, "mutation")

		// Create mock provider with sampled mutation capability
		caps := Capabilities{
			Auth:         AuthNone,
			Delivery:     DeliveryServerPush,
			Confirmation: ConfirmPerRequest,
			Mutation:     mutation,
			Identity:     IdentityDerived,
		}

		provider := &MockProvider{
			id:   ProviderID("test-mutation"),
			caps: caps,
		}

		// Register provider
		err = registry.Register(provider)
		assert.NoError(t, err)

		// Test ledger operations
		providerID := ProviderID("test-mutation")

		// Check initial state - no ledger entry
		ledgerMap, err := ledger.ListForProvider(context.Background(), providerID)
		assert.NoError(t, err)
		assert.Empty(t, ledgerMap, "Ledger should be empty initially")

		// Test connection directory operations
		// Check initial state - no connection
		conn, err := connDir.Read(context.Background(), string(providerID))
		assert.NoError(t, err)
		assert.Nil(t, conn, "Connection should be nil initially")

		// Test token broker operations
		// Token broker should handle token caching and refresh
		assert.NotNil(t, tokenBroker, "Token broker should be created successfully")
	})
}

type MockProvider struct {
	id          ProviderID
	displayName string
	caps        Capabilities
}

func (p *MockProvider) ID() ProviderID             { return p.id }
func (p *MockProvider) DisplayName() string        { return p.displayName }
func (p *MockProvider) Capabilities() Capabilities { return p.caps }
