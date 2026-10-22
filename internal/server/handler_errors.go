package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"modernc.org/sqlite"
)

// SQLite primary result codes. Extended codes keep the same low byte.
const (
	sqlitePrimaryBusy   = 5
	sqlitePrimaryLocked = 6
	sqlitePrimaryFull   = 13
)

const (
	// internalClientMessage is the sentence every unclassified 5xx returns.
	// The server log keeps the underlying error.
	internalClientMessage = "Something went wrong. Please try again."
	busyErrorCode         = "busy"
	noSpaceErrorCode      = "no_space"
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
// A lock that outlasts the busy timeout, or a full disk, is a 503.
// A 4xx stays a 4xx even when the cause is one of those database errors.
func httpStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	status := http.StatusInternalServerError
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		status = httpErr.Code
	}
	if status >= 500 && (sqliteBusy(err) || sqliteNoSpace(err)) {
		return http.StatusServiceUnavailable
	}
	return status
}

// serverClientPayload is the browser sentence for a 5xx.
// A classified busy or no-space failure keeps its own sentence and code.
// The generic sentence does not replace either of those.
func serverClientPayload(err error, code string) (string, string, bool) {
	switch {
	case sqliteBusy(err) || code == busyErrorCode:
		return clientmsg.Busy, busyErrorCode, true
	case sqliteNoSpace(err) || code == noSpaceErrorCode:
		return clientmsg.NoSpace, noSpaceErrorCode, true
	default:
		return internalClientMessage, "", false
	}
}

func sqliteBusy(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		switch driverErr.Code() & 0xff {
		case sqlitePrimaryBusy, sqlitePrimaryLocked:
			return true
		}
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

func sqliteNoSpace(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) && driverErr.Code()&0xff == sqlitePrimaryFull {
		return true
	}
	for err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "sqlite_full") || strings.Contains(msg, "database or disk is full") || strings.Contains(msg, "disk is full") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
