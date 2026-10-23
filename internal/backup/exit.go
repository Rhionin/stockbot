// Package backup snapshots pantry.db, encrypts the copy, and uploads it.
// The snapshot is staged in memory-backed storage when it fits, and on the
// data volume otherwise. Plaintext and ciphertext are removed before the
// process returns.
package backup

import (
	"errors"
	"fmt"
)

// Exit codes for stockbot-backup. A non-zero code fails the systemd unit.
const (
	ExitOK        = 0
	ExitIntegrity = 10
	ExitUpload    = 20
	ExitSweep     = 30
	ExitPreflight = 40
	ExitUsage     = 2
)

// Error is a failure the CLI can turn into an exit code and an alert class.
// Class is a short token safe to send to an alert hook. It is not a path
// and it does not include household data.
type Error struct {
	Code  int
	Class string
	Err   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Class
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func errClass(code int, class, msg string) error {
	return &Error{Code: code, Class: class, Err: errors.New(msg)}
}

func errWrap(code int, class, msg string, err error) error {
	if err == nil {
		return errClass(code, class, msg)
	}
	return &Error{Code: code, Class: class, Err: fmt.Errorf("%s: %s", msg, err.Error())}
}

// ExitCode returns the process status for err. A nil error is success.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *Error
	if errors.As(err, &coded) && coded.Code != 0 {
		return coded.Code
	}
	return ExitPreflight
}

// AlertClass returns the token sent to the failure hook.
func AlertClass(err error) string {
	var coded *Error
	if errors.As(err, &coded) && coded.Class != "" {
		return coded.Class
	}
	return "backup"
}
