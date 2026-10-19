package note

import "context"

// Repository is the persistence contract required by the note service.
//
// It is defined in the domain so MongoDB can be replaced with another document
// store (or even a relational table) without changing business rules.
type Repository interface {
	// Create stores a new note or returns ErrAlreadyExists.
	Create(ctx context.Context, note *Note) error

	// GetByID returns a note or ErrNotFound.
	GetByID(ctx context.Context, id string) (Note, error)

	// List returns notes matching the filter, newest first.
	List(ctx context.Context, filter Filter) ([]Note, error)

	// CountByTask reports how many notes reference a task. It powers the
	// cross-database overview endpoint.
	CountByTask(ctx context.Context, taskID string) (int64, error)

	// Update replaces an existing note or returns ErrNotFound.
	Update(ctx context.Context, note *Note) error

	// Delete removes a note or returns ErrNotFound.
	Delete(ctx context.Context, id string) error
}
