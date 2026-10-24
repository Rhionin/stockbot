package auth

import "context"

// Mailer would send invite and reset mail. Stage 1 does not send any,
// so links are handed to a person directly.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// NopMailer accepts a message and does not deliver it.
type NopMailer struct{}

func (NopMailer) Send(context.Context, string, string, string) error { return nil }
