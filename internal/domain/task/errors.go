package task

import "errors"

// Sentinel errors are used when callers only need to know the category of a
// failure. The HTTP layer translates them into status codes with errors.Is.
var (
	// ErrNotFound means the requested task does not exist.
	ErrNotFound = errors.New("task not found")

	// ErrAlreadyExists means a task with the same ID is already stored.
	// The service generates UUIDs, so this should be extremely rare. The
	// repository still exposes it so alternate implementations (for example a
	// database with a unique constraint) can report the same condition.
	ErrAlreadyExists = errors.New("task already exists")
)

// ValidationError describes invalid input at the field level.
//
// It is a concrete type rather than a sentinel because the HTTP layer may want
// to show which field was wrong. Callers use errors.As to extract it.
type ValidationError struct {
	Field   string // Logical field name, e.g. "title".
	Message string // Human-readable explanation, e.g. "must not be empty".
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}
