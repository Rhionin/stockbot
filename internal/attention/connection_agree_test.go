package attention

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func a11ProvisionCases() []struct {
	name string
	auth cart.AuthCapability
	conn *connection.Connection
	want string
} {
	return []struct {
		name string
		auth cart.AuthCapability
		conn *connection.Connection
		want string
	}{
		{name: "nil oauth", auth: cart.AuthOAuth2, conn: nil, want: "not_connected"},
		{name: "disconnected", auth: cart.AuthOAuth2, conn: &connection.Connection{State: connection.StateDisconnected}, want: "not_connected"},
		{name: "reauth_required", auth: cart.AuthOAuth2, conn: &connection.Connection{State: connection.StateReauthRequired}, want: "reconnect_required"},
		{name: "connected", auth: cart.AuthOAuth2, conn: &connection.Connection{State: connection.StateConnected}, want: ""},
		{name: "auth none", auth: cart.AuthNone, conn: &connection.Connection{State: connection.StateReauthRequired}, want: ""},
	}
}

// TestConnectionBlockSendAttentionAgree locks Provision to ConnectionBlock (A11-provision).
func TestConnectionBlockSendAttentionAgree(t *testing.T) {
	for _, tc := range a11ProvisionCases() {
		t.Run(tc.name, func(t *testing.T) {
			block := cart.ConnectionBlock(tc.auth, tc.conn)
			assert.Equal(t, tc.want, block)

			lineKey := shopping.ItemLineKey("a11")
			in := EvaluateInput{
				TripSeq: 1, Marks: map[string]MarkRow{},
				SendByLine: map[string][]SendResultRow{},
				Skipped:    map[string]struct{}{}, ActiveZero: map[string]struct{}{},
				PlanViews:          map[string]supply.LinePlanView{},
				UnpurchasedStaged:  map[string]int{lineKey: 1},
				PlanViewEntryByKey: map[string]PlanEntryIDs{},
				ConnFact:           Reason(block),
				Sendable:           map[string]bool{lineKey: true},
				ConnectAllowed:     true,
				ProviderID:         "fake-oauth",
			}
			perLine, _ := Evaluate(in)
			if block != "" {
				require.NotNil(t, perLine[lineKey])
				assert.Equal(t, Reason(block), perLine[lineKey].Reason)
				return
			}
			assert.Nil(t, perLine[lineKey])
			_ = context.Background()
		})
	}
}
