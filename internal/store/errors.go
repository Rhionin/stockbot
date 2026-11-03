package store

const (
	CodeInvalid       = "validation"
	CodeNotFound      = "not_found"
	CodeNameTaken     = "store_name_taken"
	CodeAlreadyLinked = "store_already_linked"
	CodeConflict      = "conflict"
)

// Error is a client-facing store failure with a registry code.
type Error struct {
	Code  string
	Msg   string
	Field string
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Code
}

func invalid(msg, field string) error {
	return &Error{Code: CodeInvalid, Msg: msg, Field: field}
}

func notFound() error {
	return &Error{Code: CodeNotFound, Msg: "Stockbot couldn't find that. It may have been removed."}
}

func nameTaken() error {
	return &Error{Code: CodeNameTaken, Msg: "You already have a store with that name.", Field: "name"}
}

func alreadyLinked() error {
	return &Error{Code: CodeAlreadyLinked, Msg: "Another store is already linked to your online cart."}
}

func conflict(msg string) error {
	return &Error{Code: CodeConflict, Msg: msg}
}
