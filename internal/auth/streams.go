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
	cancel context.CancelFunc
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
	entry := &streamCancel{cancel: cancel}
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
