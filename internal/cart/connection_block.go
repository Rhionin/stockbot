package cart

import (
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

// ConnectionBlock is the shared send/attention mapping for provider connection
// state (spec §6.4). A non-empty return means provisioning must not proceed and
// is the wire needs-attention reason when ticket C wires ConnFact.
func ConnectionBlock(auth AuthCapability, conn *connection.Connection) string {
	if auth == AuthNone {
		return ""
	}
	if conn != nil && conn.State == connection.StateConnected {
		return ""
	}
	if conn != nil && conn.State == connection.StateReauthRequired {
		return "reconnect_required"
	}
	return "not_connected"
}
