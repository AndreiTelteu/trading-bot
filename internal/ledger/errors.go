package ledger

import (
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
)

type ErrorKind string

const (
	KindValidation    ErrorKind = "validation"
	KindConflict      ErrorKind = "conflict"
	KindUnavailable   ErrorKind = "unavailable"
	KindIndeterminate ErrorKind = "indeterminate"
	KindInternal      ErrorKind = "internal"
)

type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}
func (e *Error) Unwrap() error { return e.Cause }

func newError(kind ErrorKind, code, message string, cause error) error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}
func validation(code, message string) error { return newError(KindValidation, code, message, nil) }
func conflict(code, message string, cause error) error {
	return newError(KindConflict, code, message, cause)
}
func unavailable(code, message string) error { return newError(KindUnavailable, code, message, nil) }
func internal(code string, cause error) error {
	return newError(KindInternal, code, "ledger persistence failed", cause)
}

func ErrorDetails(err error) (ErrorKind, string) {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Kind, typed.Code
	}
	return KindInternal, "internal_error"
}

func requirePrimaryAccount(account string) error {
	if account == "" || account == DefaultAccountID {
		return nil
	}
	return validation("unsupported_account", fmt.Sprintf("only account %q is supported", DefaultAccountID))
}

func normalizePersistenceError(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return conflict("duplicate_identity", "provider or ledger identity already exists", err)
		case "23503", "23514":
			return conflict("ledger_constraint", pg.ConstraintName, err)
		}
	}
	var typed *Error
	if errors.As(err, &typed) {
		return err
	}
	return internal("persistence_failure", err)
}
