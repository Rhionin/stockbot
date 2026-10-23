package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/restock"
	"modernc.org/sqlite"
)

// SQLite primary result codes. Extended codes keep the same low byte.
const (
	sqlitePrimaryBusy   = 5
	sqlitePrimaryLocked = 6
	sqlitePrimaryFull   = 13
)

const (
	busyErrorCode         = "busy"
	noSpaceErrorCode      = "no_space"
	authNotConfiguredCode = "auth_not_configured"
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
	ItemID      string
	OnHand      *int
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

// ShelfCount is a client error that also names the item and how many units are on hand.
// onHand is included even when it is 0.
func ShelfCount(status int, message, code, itemID string, onHand int) error {
	n := onHand
	return &HTTPError{
		Code:      status,
		Message:   message,
		ErrorCode: code,
		ItemID:    itemID,
		OnHand:    &n,
	}
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
// The handler ErrorCode is ignored. A 5xx without a busy or full driver
// cause stays generic, including one that already named "busy" or "no_space".
func serverClientPayload(err error, _ string) (string, string, bool) {
	switch {
	case sqliteBusy(err):
		return clientmsg.Busy, busyErrorCode, true
	case sqliteNoSpace(err):
		return clientmsg.NoSpace, noSpaceErrorCode, true
	default:
		return clientmsg.Generic, "", false
	}
}

func sqliteBusy(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		// Extended codes keep the primary in the low byte. A snapshot lock is
		// SQLITE_BUSY_SNAPSHOT (517), and 517 & 0xff == 5, so the mask
		// classifies it. Once the driver error is in hand, its text is not
		// a second vote.
		switch driverErr.Code() & 0xff {
		case sqlitePrimaryBusy, sqlitePrimaryLocked:
			return true
		default:
			return false
		}
	}
	// A commit failure already chose code internal and a sentence. The string
	// fallback must not replace that decision. A driver result code is
	// classified above and still wins.
	if keepsInternalCode(err) {
		return false
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

// logNoSpace is the one server line for a no-space 503. Operator alerts
// match msg and event. The warning carries no driver text and no SQL.
// The wrapped message is a second record at debug level.
func logNoSpace(r *http.Request, err error) {
	logStorageWarning(r, err, "db_no_space")
	if msg := noSpaceDriverMessage(err); msg != "" {
		slog.Debug("db_no_space", "driver_message", msg)
	}
}

// logBusy is the one server line for a batch that ends busy.
// Operator alerts match msg and event. The warning carries no driver text and no SQL.
func logBusy(r *http.Request, err error) {
	logStorageWarning(r, err, "db_busy")
}

func logStorageWarning(r *http.Request, err error, event string) {
	method, route, household := "", "", ""
	if r != nil {
		method = r.Method
		route = requestRoute(r)
		// One household serves every request until logins name another.
		// The id is not read from the query or body.
		household = restock.HouseholdID
	}
	attrs := []any{"event", event, "method", method, "route", route}
	if household != "" {
		attrs = append(attrs, "household_id", household)
	}
	if code := sqliteDriverCode(err); code != 0 {
		attrs = append(attrs, "sqlite_code", code)
	}
	slog.Warn(event, attrs...)
}

// noSpaceDriverMessage is the driver text for the debug record.
// HTTPError.Error returns the client sentence once Message is set, which is
// what a commit or stock-out failure does, so the debug line uses the wrapped cause.
func noSpaceDriverMessage(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Err != nil {
		return httpErr.Err.Error()
	}
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		return driverErr.Error()
	}
	return err.Error()
}

func requestRoute(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func sqliteDriverCode(err error) int {
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) {
		return 0
	}
	return driverErr.Code()
}

func sqliteNoSpace(err error) bool {
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		return driverErr.Code()&0xff == sqlitePrimaryFull
	}
	if keepsInternalCode(err) {
		return false
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

// keepsInternalCode reports an unexpected failure whose sentence and code
// were already chosen. Only the string fallback consults it.
func keepsInternalCode(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.ErrorCode == inventory.CodeInternal && httpErr.Message != ""
}
