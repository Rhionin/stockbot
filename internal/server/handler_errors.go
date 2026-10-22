package server

import (
	"errors"
	"net/http"
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
func httpStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}

	return http.StatusInternalServerError
}
