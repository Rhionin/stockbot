package auth

import (
	"context"
	"sync"
	"time"
)

// streamCap is how many open event streams one principal may hold.
// The eleventh is refused so one client cannot pin the process.
const streamCap = 10

type streamCancel struct {
	cancel      context.CancelFunc
	householdID string
	userID      string
	kind        string
}

// Streams cancels GET /api/events when a person is signed out, removed,
// or has their password reset.
type Streams struct {
	mu    sync.Mutex
	byKey map[string]map[*streamCancel]struct{}
}

func NewStreams() *Streams {
	return &Streams{byKey: map[string]map[*streamCancel]struct{}{}}
}

// Bind attaches ctx to key. The eleventh live stream for that key is refused.
func (s *Streams) Bind(ctx context.Context, key string) (context.Context, error) {
	return s.BindLive(ctx, key, "", "", "")
}

// BindLive attaches ctx to key and records who the stream belongs to.
// KickHousehold uses that record. The eleventh live stream for key is refused.
func (s *Streams) BindLive(ctx context.Context, key, householdID, userID, kind string) (context.Context, error) {
	if s == nil || key == "" {
		return ctx, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	live := s.byKey[key]
	if len(live) >= streamCap {
		return nil, rateLimited(time.Second)
	}
	ctx, cancel := context.WithCancel(ctx)
	entry := &streamCancel{cancel: cancel, householdID: householdID, userID: userID, kind: kind}
	if live == nil {
		live = map[*streamCancel]struct{}{}
		s.byKey[key] = live
	}
	live[entry] = struct{}{}
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.byKey[key], entry)
		if len(s.byKey[key]) == 0 {
			delete(s.byKey, key)
		}
		s.mu.Unlock()
	}()
	return ctx, nil
}

// KickHousehold closes streams for one member of a household.
// An empty household closes that user's streams in every household.
// An empty user closes household-password streams in the household.
func (s *Streams) KickHousehold(householdID, userID string) {
	if s == nil || (householdID == "" && userID == "") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, live := range s.byKey {
		for entry := range live {
			if streamMatch(entry, householdID, userID) {
				entry.cancel()
			}
		}
	}
}

func streamMatch(entry *streamCancel, householdID, userID string) bool {
	if entry == nil {
		return false
	}
	if userID != "" && householdID != "" {
		return entry.userID == userID && entry.householdID == householdID
	}
	if userID != "" {
		return entry.userID == userID
	}
	return entry.householdID == householdID && entry.kind == "household_password"
}

// Kick ends every stream registered under key.
func (s *Streams) Kick(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for entry := range s.byKey[key] {
		entry.cancel()
	}
}

// UserStreamKey is the events bucket for one account.
func UserStreamKey(userID string) string {
	if userID == "" {
		return ""
	}
	return "user:" + userID
}

// HouseholdPasswordStreamKey is the bucket closed when that password is retired.
func HouseholdPasswordStreamKey() string { return "hhpw" }
