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

// BadRequestDetails is a 400 whose body also carries a machine-readable code.
func BadRequestDetails(message, code string) error {
	return &HTTPError{Code: http.StatusBadRequest, Message: message, ErrorCode: code}
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
