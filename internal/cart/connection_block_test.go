package cart

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type a11Case struct {
	name   string
	auth   AuthCapability
	conn   *connection.Connection
	want   string
	seedDB bool // when true, write conn to directory and run Provision
}

func a11Cases() []a11Case {
	disconn := &connection.Connection{Provider: "fake-oauth", State: connection.StateDisconnected}
	reauth := &connection.Connection{Provider: "fake-oauth", State: connection.StateReauthRequired}
	future := time.Now().UTC().Add(2 * time.Hour)
	connected := &connection.Connection{
		Provider:     "fake-oauth",
		State:        connection.StateConnected,
		AccessToken:  "valid",
		RefreshToken: "refresh-1",
		ExpiresAt:    &future,
	}
	notRequired := &connection.Connection{Provider: "fake-oauth", State: connection.StateNotRequired}
	unknownNorm := &connection.Connection{Provider: "fake-oauth", State: connection.StateDisconnected}

	return []a11Case{
		{name: "nil oauth", auth: AuthOAuth2, conn: nil, want: "not_connected", seedDB: true},
		{name: "disconnected", auth: AuthOAuth2, conn: disconn, want: "not_connected", seedDB: true},
		{name: "empty normalized", auth: AuthOAuth2, conn: unknownNorm, want: "not_connected", seedDB: true},
		{name: "not_required oauth", auth: AuthOAuth2, conn: notRequired, want: "not_connected", seedDB: true},
		{name: "reauth_required", auth: AuthOAuth2, conn: reauth, want: "reconnect_required", seedDB: true},
		{name: "connected", auth: AuthOAuth2, conn: connected, want: "", seedDB: true},
		{name: "auth none nil", auth: AuthNone, conn: nil, want: "", seedDB: false},
		{name: "auth none disconnected", auth: AuthNone, conn: disconn, want: "", seedDB: false},
		{name: "auth none reauth", auth: AuthNone, conn: reauth, want: "", seedDB: false},
	}
}

func TestConnectionBlock(t *testing.T) {
	for _, tc := range a11Cases() {
		t.Run(tc.name, func(t *testing.T) {
			got := ConnectionBlock(tc.auth, tc.conn)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestConnectionBlockProvisionAgree locks Engine.Provision to ConnectionBlock (engine golden).
func TestConnectionBlockProvisionAgree(t *testing.T) {
	cases := a11Cases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := ConnectionBlock(tc.auth, tc.conn)
			assert.Equal(t, tc.want, block)

			if !tc.seedDB {
				return
			}

			env := newProvisionEnv(t)
			ctx := context.Background()

			caps := derivedNoneCaps(ConfirmPerRequest)
			caps.Auth = tc.auth
			providerID := ProviderID("fake-oauth")
			provider := newFakeProvider(providerID, caps, &fakeScript{
				identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
			})
			require.NoError(t, env.registry.Register(provider))
			env.seedShortfall(t, "prod-1", "Milk", "0001", 1)

			if tc.conn != nil {
				toWrite := *tc.conn
				toWrite.Provider = string(providerID)
				require.NoError(t, env.directory.Write(ctx, env.scope, &toWrite))
			}

			_, err := env.engine.Provision(ctx, env.scope, providerID)
			if block != "" {
				var conflict *ProvisionConflict
				require.ErrorAs(t, err, &conflict)
				assert.Equal(t, `provider "fake-oauth" is not connected`, conflict.Reason)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
