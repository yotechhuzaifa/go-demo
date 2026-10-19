package note

import "errors"

// Sentinel errors are used when callers only need to know the category of a
// failure. The HTTP layer translates them into status codes with errors.Is.
var (
	// ErrNotFound means the requested note does not exist.
	ErrNotFound = errors.New("note not found")

	// ErrAlreadyExists means a note with the same ID is already stored.
	ErrAlreadyExists = errors.New("note already exists")
)

// ValidationError describes invalid input at the field level.
type ValidationError struct {
	Field   string // Logical field name, e.g. "body".
	Message string // Human-readable explanation.
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}
