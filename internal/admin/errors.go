package admin

import "fmt"

// ErrorKind classifies an admin-call failure so the import wizard can render
// the right copy without string-matching the underlying error.
type ErrorKind string

const (
	ErrUnreachable ErrorKind = "unreachable"
	ErrAuth        ErrorKind = "auth"
	ErrOther       ErrorKind = "other"
)

// Error wraps an admin-call failure with a kind classifier.
type Error struct {
	Kind  ErrorKind
	Cause error
}

func (e *Error) Error() string {
	if e == nil || e.Cause == nil {
		return "admin: " + string(e.Kind)
	}
	return fmt.Sprintf("admin %s: %v", e.Kind, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }
