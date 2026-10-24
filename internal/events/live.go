package events

import "context"

// LiveHint is a refetch signal. Type is scans, stock, list, devices, or rekey.
// Cursor is an opaque changes-since token. The JSON stays under 1 KB and
// carries no item, product, or person names.
type LiveHint struct {
	Type   string `json:"type"`
	Cursor string `json:"cursor,omitempty"`
}

// Principal is who holds a live stream. Kind is account, household_password, or lan.
type Principal struct {
	Kind        string
	UserID      string
	HouseholdID string
	SourceIP    string
}

// Event is one frame delivered to a LiveHub subscriber.
type Event struct {
	EventType   string
	Data        []byte
	HouseholdID string
}

// Legacy is an existing SSE payload published with a hint.
type Legacy struct {
	Event   string
	Payload any
}

// LiveHub is the Stage-1 live channel. Publish runs after the write commits.
// Kick closes streams for a removed member, a revoked session, or a retired
// household password.
type LiveHub interface {
	Publish(ctx context.Context, householdID string, h LiveHint, legacy any)
	Subscribe(p Principal) (events <-chan Event, cancel func())
	Kick(householdID, userID string)
}
