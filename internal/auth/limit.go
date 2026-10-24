package auth

import (
	"sync"
	"time"
)

// Limiter counts failures inside a window. A call that is already at the
// cap does not add another hit, so the retry does not move further out.
type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{hits: map[string][]time.Time{}}
}

const (
	loginIPMax     = 5
	loginIPWindow  = 15 * time.Minute
	loginMailMax   = 10
	loginMailWin   = 15 * time.Minute
	inviteIPMax    = 10
	inviteIPWindow = 15 * time.Minute
	pairIPMax      = 10
	pairIPWindow   = 10 * time.Minute
)

// Blocked reports a cap that was already reached. It does not record a hit.
func (l *Limiter) Blocked(now time.Time, key string, max int, window time.Duration) (time.Duration, bool) {
	if l == nil || key == "" {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now, key, window)
	if len(l.hits[key]) >= max {
		return l.retryLocked(now, key, window), true
	}
	return 0, false
}

// Hit records one failure unless the cap is already reached.
func (l *Limiter) Hit(now time.Time, key string, max int, window time.Duration) (time.Duration, bool) {
	if l == nil || key == "" {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now, key, window)
	if len(l.hits[key]) >= max {
		return l.retryLocked(now, key, window), true
	}
	l.hits[key] = append(l.hits[key], now)
	return 0, false
}

// Reset clears one key after a success.
func (l *Limiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

func (l *Limiter) prune(now time.Time, key string, window time.Duration) {
	hits := l.hits[key]
	cut := now.Add(-window)
	kept := hits[:0]
	for _, at := range hits {
		if at.After(cut) {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return
	}
	l.hits[key] = kept
}

func (l *Limiter) retryLocked(now time.Time, key string, window time.Duration) time.Duration {
	hits := l.hits[key]
	if len(hits) == 0 {
		return time.Second
	}
	oldest := hits[0]
	for _, at := range hits[1:] {
		if at.Before(oldest) {
			oldest = at
		}
	}
	retry := oldest.Add(window).Sub(now)
	if retry < time.Second {
		retry = time.Second
	}
	return retry
}

func LoginIPKey(ip string) string { return "login-ip:" + ip }

func LoginMailKey(email string) string { return "login-email:" + email }

func InviteIPKey(ip string) string { return "invite-ip:" + ip }

func ResetIPKey(ip string) string { return "reset-ip:" + ip }

func PairIPKey(ip string) string { return "pair-ip:" + ip }

func (s *Service) blockKey(key string, max int, window time.Duration) error {
	if s == nil {
		return nil
	}
	if wait, blocked := s.limits.Blocked(s.clock(), key, max, window); blocked {
		return rateLimited(wait)
	}
	return nil
}

func (s *Service) hitKey(key string, max int, window time.Duration) error {
	if s == nil || key == "" {
		return nil
	}
	if wait, blocked := s.limits.Hit(s.clock(), key, max, window); blocked {
		return rateLimited(wait)
	}
	return nil
}

// BlockLoginIP reports a per-address login cap without recording another miss.
func (s *Service) BlockLoginIP(ip string) error {
	return s.blockKey(LoginIPKey(ip), loginIPMax, loginIPWindow)
}

// FailLoginIP records one login or setup miss for this address.
func (s *Service) FailLoginIP(ip string) error {
	return s.hitKey(LoginIPKey(ip), loginIPMax, loginIPWindow)
}

// BlockLoginEmail reports a per-email login cap. An address that is not an
// email is ignored so a malformed body is not a miss.
func (s *Service) BlockLoginEmail(email string) error {
	norm, ok := normalizeEmail(email)
	if !ok {
		return nil
	}
	return s.blockKey(LoginMailKey(norm), loginMailMax, loginMailWin)
}

// FailLoginEmail records one miss for this email address.
func (s *Service) FailLoginEmail(email string) error {
	norm, ok := normalizeEmail(email)
	if !ok {
		return nil
	}
	return s.hitKey(LoginMailKey(norm), loginMailMax, loginMailWin)
}

// ClearLogin forgets misses after a successful sign-in.
func (s *Service) ClearLogin(ip, email string) {
	if s == nil {
		return
	}
	s.limits.Reset(LoginIPKey(ip))
	if norm, ok := normalizeEmail(email); ok {
		s.limits.Reset(LoginMailKey(norm))
	}
}

// BlockInviteIP reports the invite and reset cap for this address.
func (s *Service) BlockInviteIP(ip string) error {
	return s.blockKey(InviteIPKey(ip), inviteIPMax, inviteIPWindow)
}

// NoteInviteIP records one well-formed invite, reset, or household-create attempt.
func (s *Service) NoteInviteIP(ip string) error {
	return s.hitKey(InviteIPKey(ip), inviteIPMax, inviteIPWindow)
}

// BlockResetIP reports the reset cap. Reset shares the invite window.
func (s *Service) BlockResetIP(ip string) error {
	return s.blockKey(ResetIPKey(ip), inviteIPMax, inviteIPWindow)
}

// NoteResetIP records one well-formed reset attempt.
func (s *Service) NoteResetIP(ip string) error {
	return s.hitKey(ResetIPKey(ip), inviteIPMax, inviteIPWindow)
}

// BlockPairIP reports the pairing cap for this address.
func (s *Service) BlockPairIP(ip string) error {
	return s.blockKey(PairIPKey(ip), pairIPMax, pairIPWindow)
}

// NotePairIP records one well-formed pairing attempt.
func (s *Service) NotePairIP(ip string) error {
	return s.hitKey(PairIPKey(ip), pairIPMax, pairIPWindow)
}
