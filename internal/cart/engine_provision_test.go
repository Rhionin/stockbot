package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// derivedNoneCaps is the simplest provisioning profile: no auth, server push,
// per-request confirmation, add-only, identity derived from the barcode.
func derivedNoneCaps(confirm ConfirmationCapability) Capabilities {
	return Capabilities{
		Auth:         AuthNone,
		Delivery:     DeliveryServerPush,
		Confirmation: confirm,
		Mutation:     MutateAddOnly,
		Identity:     IdentityDerived,
	}
}

func TestReplenishmentMode_Valid(t *testing.T) {
	tests := []struct {
		mode ReplenishmentMode
		want bool
	}{
		{ReplenishMode, true},
		{TargetMode, true},
		{ReplenishmentMode("weekly"), false},
		{ReplenishmentMode(""), false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.mode.Valid())
	}
}

func TestNewEngine_SettersWireCollaborators(t *testing.T) {
	env := newProvisionEnv(t)

	// A provider driven end to end through the freshly wired engine confirms the
	// setters connected every collaborator: the shopping list, pantry, catalog,
	// and consumption log all feed getComputedEntries.
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 1, report.Confirmed)
}

func TestProvision_UnregisteredProvider(t *testing.T) {
	env := newProvisionEnv(t)

	_, err := env.engine.Provision(context.Background(), env.scope, "ghost")

	require.Error(t, err)
	assert.Equal(t, `provider "ghost" not registered`, err.Error())
}

func TestProvision_NotConnected(t *testing.T) {
	env := newProvisionEnv(t)

	caps := derivedNoneCaps(ConfirmPerRequest)
	caps.Auth = AuthOAuth2
	provider := newFakeProvider("fake-oauth", caps, &fakeScript{})
	require.NoError(t, env.registry.Register(provider))

	_, err := env.engine.Provision(context.Background(), env.scope, "fake-oauth")

	require.Error(t, err)
	assert.Equal(t, `provider "fake-oauth" is not connected`, err.Error())
}

func TestProvision_PerRequest_ConfirmsAndAdvancesLedger(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, ProviderID("fake-none"), report.Provider)
	assert.Equal(t, 1, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeConfirmed,
		Reason:   "",
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 2, ledger[itemID].Requested)
}

func TestProvision_PerLine_FollowsResultMap(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerLine)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		perLine:       map[ProductIdentity]bool{"SKU-1": true},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 3)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 3,
		Outcome:  OutcomeConfirmed,
	}, report.Entries[0])
	assert.Equal(t, 1, report.Confirmed)

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 3, ledger[itemID].Requested)
}

func TestProvision_ConfirmNone_YieldsUnknownWithReason(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmNone)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 0, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeUnknown,
		Reason:   ReasonNoPerItemResult,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 0, ledger[itemID].Requested, "confirmation=none advances no ledger")
}

func TestProvision_Rejected_FailsAndLeavesLedgerUntouched(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionRejected},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 0, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeFailed,
		Reason:   ReasonProviderRejected,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 0, ledger[itemID].Requested)
}

func TestProvision_Indeterminate_UnknownAndLeavesLedgerUntouched(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionIndeterminate},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 0, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeUnknown,
		Reason:   ReasonRequestIncomplete,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 0, ledger[itemID].Requested)
}

func TestProvision_NoResolvableIdentity_FailsWithNoProductIdentity(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	// The script resolves no barcode, so the entry's identity stays unresolved.
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		identityByBar: map[string]ProductIdentity{},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 0, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeFailed,
		Reason:   ReasonNoProductIdentity,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 0, ledger[itemID].Requested, "an unidentified entry submits no line")
}

// TestProvision_PerRequest_SharedIdentityCountsEachEntry drives two entries
// whose products resolve to one provider identity. They batch into a single
// line with two accounts, and Confirmed counts each entry once, not the square
// of the account count.
func TestProvision_PerRequest_SharedIdentityCountsEachEntry(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions: []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{
			"0001": "SHARED-SKU",
			"0002": "SHARED-SKU",
		},
	})
	require.NoError(t, env.registry.Register(provider))
	itemA, _ := env.seedShortfall(t, "prod-a", "Milk", "0001", 2)
	itemB, _ := env.seedShortfall(t, "prod-b", "Cream", "0002", 1)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 2, report.Confirmed)
	require.Len(t, report.Entries, 2)
	for _, entry := range report.Entries {
		assert.Equal(t, OutcomeConfirmed, entry.Outcome)
	}

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	// Each account advances by its own quantity, so the shared line records 2 + 1.
	advanced := ledger[itemA].Requested + ledger[itemB].Requested
	assert.Equal(t, 3, advanced)
}

// TestProvision_LookedUpIdentity resolves identities through the provider's
// LookUpIdentity call rather than a local derivation, the other identity path.
func TestProvision_LookedUpIdentity(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	caps.Identity = IdentityLookedUp
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "LOOKED-UP-SKU"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	assert.Equal(t, 1, report.Confirmed)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeConfirmed,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 2, ledger[itemID].Requested)
}

// TestProvision_ConcurrencyGate holds the in-flight slot with a blocking Add on
// the first Provision, then asserts a second concurrent Provision for the same
// provider returns the already-in-progress error rather than running.
func TestProvision_ConcurrencyGate(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	script := &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		addBlock:      make(chan struct{}),
		addStarted:    make(chan struct{}),
	}
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, script)
	require.NoError(t, env.registry.Register(provider))
	env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	firstDone := make(chan error, 1)
	go func() {
		_, err := env.engine.Provision(ctx, env.scope, "fake-none")
		firstDone <- err
	}()

	<-script.addStarted // the first call now holds the in-flight slot

	_, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.Error(t, err)
	assert.Equal(t, `provisioning operation already in progress for provider "fake-none"`, err.Error())

	close(script.addBlock)
	require.NoError(t, <-firstDone)
}

func TestProvision_PerLine_RejectedLineDoesNotAdvanceLedger(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerLine)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions: []ResultDisposition{DispositionAccepted},
		perLine: map[ProductIdentity]bool{
			"SKU-OK": true,
			"SKU-NO": false,
		},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-OK", "0002": "SKU-NO"},
	})
	require.NoError(t, env.registry.Register(provider))
	okItem, _ := env.seedShortfall(t, "prod-ok", "Milk", "0001", 2)
	noItem, _ := env.seedShortfall(t, "prod-no", "Bread", "0002", 4)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 1, report.Confirmed)

	byItem := map[string]EntryOutcome{}
	for _, entry := range report.Entries {
		byItem[entry.ItemID] = entry
	}
	assert.Equal(t, OutcomeConfirmed, byItem[okItem].Outcome)
	assert.Equal(t, OutcomeFailed, byItem[noItem].Outcome)
	assert.Equal(t, ReasonProviderRejected, byItem[noItem].Reason)

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 2, ledger[okItem].Requested)
	_, rejectedAdvanced := ledger[noItem]
	assert.False(t, rejectedAdvanced)
}

func TestProvision_BatchSizeSplitsRequests(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted, DispositionRejected},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1", "0002": "SKU-2"},
	})
	require.NoError(t, env.registry.Register(provider, WithBatchSize(1)))
	env.seedShortfall(t, "prod-1", "Milk", "0001", 2)
	env.seedShortfall(t, "prod-2", "Bread", "0002", 3)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 1, report.Confirmed)

	var confirmed, failed int
	for _, entry := range report.Entries {
		switch entry.Outcome {
		case OutcomeConfirmed:
			confirmed++
		case OutcomeFailed:
			failed++
		}
	}
	assert.Equal(t, 1, confirmed)
	assert.Equal(t, 1, failed)
}

type handoffFake struct {
	*fakeProvider
	artifact HandoffArtifact
}

func (h *handoffFake) BuildHandoff(ProvisionRequest) (HandoffArtifact, error) {
	return h.artifact, nil
}

func TestProvision_ClientHandoff_RecordsUnknownAndDoesNotAdvanceLedger(t *testing.T) {
	env := newProvisionEnv(t)
	ctx := context.Background()

	caps := derivedNoneCaps(ConfirmPerRequest)
	caps.Delivery = DeliveryClientHandoff
	inner := newFakeProvider("fake-none", caps, &fakeScript{
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	provider := &handoffFake{
		fakeProvider: inner,
		artifact:     HandoffArtifact{URL: "https://example.test/cart"},
	}
	require.NoError(t, env.registry.Register(provider))
	itemID, entryID := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	report, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	assert.Equal(t, 0, report.Confirmed)
	require.NotNil(t, report.Handoff)
	assert.Equal(t, "https://example.test/cart", report.Handoff.URL)
	require.Len(t, report.Entries, 1)
	assert.Equal(t, EntryOutcome{
		EntryID:  entryID,
		ItemID:   itemID,
		Name:     "Milk",
		Quantity: 2,
		Outcome:  OutcomeUnknown,
		Reason:   ReasonNoPerItemResult,
	}, report.Entries[0])

	ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
	require.NoError(t, err)
	_, advanced := ledger[itemID]
	assert.False(t, advanced)
}
