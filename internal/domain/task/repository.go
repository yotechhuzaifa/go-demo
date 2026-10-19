package task

import "context"

// Repository is the persistence contract required by the domain service.
//
// Defining the interface here (where it is consumed) is idiomatic Go: the
// domain describes what it needs, and infrastructure adapters such as the
// in-memory store or a future PostgreSQL store satisfy it. This also makes it
// trivial to replace the repository with a fake in unit tests.
//
// Every method accepts a context so future implementations can respect
// cancellation, deadlines, and tracing without changing the interface.
type Repository interface {
	// Create stores a new task. It returns ErrAlreadyExists if the ID is taken.
	Create(ctx context.Context, task *Task) error

	// GetByID returns a task or ErrNotFound.
	GetByID(ctx context.Context, id string) (Task, error)

	// List returns tasks matching the filter. Implementations should return a
	// deterministic order so API responses are stable.
	List(ctx context.Context, filter Filter) ([]Task, error)

	// Update replaces an existing task or returns ErrNotFound.
	Update(ctx context.Context, task *Task) error

	// Delete removes a task or returns ErrNotFound.
	Delete(ctx context.Context, id string) error
}
