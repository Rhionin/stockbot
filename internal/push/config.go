package push

import (
	"context"
	"encoding/base64"
	"strings"
	"time"
)

// BatchSettle is the quiet period before a stock in or stock out batch is
// announced. It matches the gap that splits scan sessions in the queue.
const BatchSettle = 5 * time.Minute

// Config is the VAPID identity this pantry uses to sign push messages.
type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string
	// Settle overrides BatchSettle. Zero keeps the five-minute wait.
	Settle time.Duration
	// Post replaces the push network call. Production leaves it nil.
	Post func(ctx context.Context, sub Subscription, payload []byte) (status int, err error)
	// PublicHost is the hostname that should receive alerts. Empty means every
	// subscription except the previous public host.
	PublicHost string
	// LegacyHost is the previous public hostname. Subscriptions from it are
	// dropped. A value equal to PublicHost, or one that includes a scheme, is ignored.
	LegacyHost string
	// AppName is the product name used when an alert has no title of its own.
	// Empty uses the configured default.
	AppName string
}

// ConfigFromEnv reads VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY, VAPID_SUBJECT,
// PUBLIC_HOST, and LEGACY_PUBLIC_HOST.
func ConfigFromEnv(getenv func(string) string) Config {
	if getenv == nil {
		return Config{}
	}
	return Config{
		PublicKey:  strings.TrimSpace(getenv("VAPID_PUBLIC_KEY")),
		PrivateKey: strings.TrimSpace(getenv("VAPID_PRIVATE_KEY")),
		Subject:    strings.TrimSpace(getenv("VAPID_SUBJECT")),
		PublicHost: strings.TrimSpace(getenv("PUBLIC_HOST")),
		LegacyHost: strings.TrimSpace(getenv("LEGACY_PUBLIC_HOST")),
	}
}

// Configured reports whether both keys are a P-256 VAPID pair.
func (c Config) Configured() bool {
	return validPublicKey(c.PublicKey) && validPrivateKey(c.PrivateKey)
}

// CanSend is true when a message can leave this process, including a test
// double that does not talk to a push service.
func (c Config) CanSend() bool {
	if c.Post != nil {
		return true
	}
	return c.Configured()
}

func (c Config) settle() time.Duration {
	if c.Settle > 0 {
		return c.Settle
	}
	return BatchSettle
}

func (c Config) subject() string {
	subject := strings.TrimSpace(c.Subject)
	if strings.HasPrefix(subject, "mailto:") || strings.HasPrefix(subject, "https://") {
		return subject
	}
	return "mailto:pantry@localhost"
}

func validPublicKey(value string) bool {
	raw, ok := decodeKey(value)
	return ok && len(raw) == 65 && raw[0] == 0x04
}

func validPrivateKey(value string) bool {
	raw, ok := decodeKey(value)
	return ok && len(raw) == 32
}

func decodeKey(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(value)
		if err != nil {
			return nil, false
		}
	}
	return raw, true
}
