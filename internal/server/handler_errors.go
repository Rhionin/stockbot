package server

import (
	"errors"
	"net/http"
	"strings"

	"modernc.org/sqlite"
)

// sqlitePrimaryBusy is SQLite's SQLITE_BUSY primary result code. Extended
// codes such as SQLITE_BUSY_TIMEOUT keep this low byte.
const sqlitePrimaryBusy = 5

const (
	// internalClientMessage is the sentence every other 5xx returns. The
	// server log keeps the underlying error.
	internalClientMessage = "Something went wrong. Please try again."
	// busyClientMessage is the sentence after the busy timeout. The client
	// can retry; the raw driver text stays in the log.
	busyClientMessage = "The pantry is busy right now. Please try again."
)

// HTTPError represents an error with an associated HTTP status code.
// ErrorCode, Members, and Suggestions are included in the JSON body when set,
// so a 409 can name the decision the client has to make.
type HTTPError struct {
	Code        int
	Message     string
	Err         error
	ErrorCode   string
	Members     any
	Suggestions any
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return http.StatusText(e.Code)
}

func (e *HTTPError) Unwrap() error {
	return e.Err
}

// Common HTTP error constructors.

func BadRequest(message string) error {
	return &HTTPError{Code: http.StatusBadRequest, Message: message}
}

func NotFound(message string) error {
	return &HTTPError{Code: http.StatusNotFound, Message: message}
}

func Conflict(message string) error {
	return &HTTPError{Code: http.StatusConflict, Message: message}
}

// ConflictDetails is a 409 whose body also carries a machine-readable code and members.
func ConflictDetails(message, code string, members any) error {
	return &HTTPError{Code: http.StatusConflict, Message: message, ErrorCode: code, Members: members}
}

// ConflictSuggestions is a 409 whose body carries a machine-readable code and link suggestions.
func ConflictSuggestions(message, code string, suggestions any) error {
	return &HTTPError{Code: http.StatusConflict, Message: message, ErrorCode: code, Suggestions: suggestions}
}

func InternalError(err error) error {
	return &HTTPError{Code: http.StatusInternalServerError, Err: err}
}

func BadGateway(message string) error {
	return &HTTPError{Code: http.StatusBadGateway, Message: message}
}

// httpStatusFromError determines the HTTP status code from an error.
// A lock that outlasts the busy timeout is a 503. Other server errors stay 500.
func httpStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	status := http.StatusInternalServerError
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		status = httpErr.Code
	}
	if status >= 500 && sqliteBusy(err) {
		return http.StatusServiceUnavailable
	}
	return status
}

// clientMessage is the fixed sentence a browser sees for a 5xx. Database text
// is not included. A busy lock names the retry; every other 5xx stays generic.
func clientMessage(err error) string {
	if sqliteBusy(err) {
		return busyClientMessage
	}
	return internalClientMessage
}

func sqliteBusy(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) && driverErr.Code()&0xff == sqlitePrimaryBusy {
		return true
	}
	for err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
