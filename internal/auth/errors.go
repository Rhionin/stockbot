package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"modernc.org/sqlite"
)

const (
	CodeUnauthorized   = "unauthorized"
	CodeForbiddenRole  = "forbidden_role"
	CodeNotFound       = "not_found"
	CodeValidation     = "validation"
	CodeRateLimited    = "rate_limited"
	CodeBusy           = "busy"
	CodeNoSpace        = "no_space"
	CodeInviteExpired  = "invite_expired"
	CodeResetExpired   = "reset_expired"
	CodeAccountExists  = "account_exists"
	CodeLastOwner      = "last_owner"
	CodeInviteRequired = "invite_required"
	CodeDeviceRevoked  = "device_revoked"
	CodeUpgrade        = "upgrade_required"
	CodeConfirm        = "confirmation_mismatch"
	CodeConflict       = "conflict"
	CodeTooLarge       = "payload_too_large"

	SentenceUnauthorized   = "Sign in to continue."
	SentenceForbidden      = "Only an owner can do that."
	SentenceNotFound       = "Stockbot couldn't find that. It may have been removed."
	SentenceValidation     = "Check what you entered and try again."
	SentenceRate           = "Too many tries. Wait a moment and try again."
	SentenceInviteExpired  = "This invite has expired or was already used. Ask for a new one."
	SentenceResetExpired   = "This reset link has expired or was already used. Ask for a new one."
	SentenceAccountExists  = "That email already has an account. Sign in, then open the invite again."
	SentenceLastOwner      = "You're the only owner. Make someone else an owner first, or delete the household."
	SentenceInviteRequired = "You need an invite to start a household."
	SentenceDeviceRevoked  = "This device was disconnected. Pair it again to keep using it."
	SentenceUpgrade        = "Update Stockbot to keep using it."
	SentenceConfirm        = "That doesn't match. Check the spelling and try again."
	SentenceConflict       = "Someone else just changed this. Refresh and try again."
	SentenceTooLarge       = "That file is too large to import."
	SentencePasswordShort  = "Use at least 10 characters."
	SentenceDeleteHouse    = "Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that."
)

// ErrBusy is the argon2 wait and a database lock that outlasted the timeout.
var ErrBusy = errors.New(clientmsg.Busy)

// Error is a failure the HTTP layer can show to a person.
// Count is set when a well-formed credential attempt should hit a rate limit.
type Error struct {
	Code    string
	Message string
	Field   string
	Retry   time.Duration
	Count   bool
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func failField(code, message, field string) *Error {
	return &Error{Code: code, Message: message, Field: field}
}

func failCount(code, message string) *Error {
	return &Error{Code: code, Message: message, Count: true}
}

func unauthorized() *Error { return fail(CodeUnauthorized, SentenceUnauthorized) }

func notFound() *Error { return failCount(CodeNotFound, SentenceNotFound) }

func validation() *Error { return fail(CodeValidation, SentenceValidation) }

func busy(err error) *Error {
	return &Error{Code: CodeBusy, Message: clientmsg.Busy, Err: err}
}

func noSpace(err error) *Error {
	return &Error{Code: CodeNoSpace, Message: clientmsg.NoSpace, Err: err}
}

func rateLimited(retry time.Duration) *Error {
	if retry < time.Second {
		retry = time.Second
	}
	return &Error{Code: CodeRateLimited, Message: SentenceRate, Retry: retry}
}

// AsError returns a classified error, including a database lock or a full disk.
// A nil error stays nil. Callers that need the code use errors.As.
func AsError(err error) error {
	if err == nil {
		return nil
	}
	return classify(err)
}

func classify(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, ErrBusy) || sqliteBusy(err) {
		return busy(err)
	}
	if sqliteNoSpace(err) {
		return noSpace(err)
	}
	return &Error{Code: "internal", Message: clientmsg.Generic, Err: err}
}

func sqliteBusy(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		switch driverErr.Code() & 0xff {
		case 5, 6:
			return true
		default:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

func sqliteNoSpace(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		return driverErr.Code()&0xff == 13
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database or disk is full") || strings.Contains(msg, "sqlite_full")
}
