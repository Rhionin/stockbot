package auth

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

func passwordOK(password string) *Error {
	n := len(password)
	if n < 10 {
		return failField(CodeValidation, SentencePasswordShort, "password")
	}
	if n > 256 {
		return failField(CodeValidation, SentenceValidation, "password")
	}
	return nil
}

func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") || !strings.Contains(email, "@") {
		return "", false
	}
	return email, true
}

func normalizeName(raw string, field string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n") {
		return "", failField(CodeValidation, SentenceValidation, field)
	}
	return name, nil
}

func normalizeRestock(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "", "auto":
		return "auto", true
	case "ask_restock", "ask_hold":
		return raw, true
	default:
		return "", false
	}
}

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return strings.ToLower(uuid.NewString())
	}
	return id.String()
}

func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func parseStamp(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func strPtr(raw string) *string {
	if raw == "" {
		return nil
	}
	s := raw
	return &s
}

const (
	sessionIdle  = 30 * 24 * time.Hour
	sessionCap   = 180 * 24 * time.Hour
	sessionSlide = time.Hour
	codeLife     = 24 * time.Hour
	inviteLife   = 7 * 24 * time.Hour
	pairLife     = 10 * time.Minute
	onlineWindow = 300 * time.Second
	presenceSave = 5 * time.Minute
	tokenRotate  = 30 * 24 * time.Hour
	tokenGrace   = 24 * time.Hour
	scanLimit    = 600
	scanWindow   = time.Minute
	maxAttempts  = 5
)
