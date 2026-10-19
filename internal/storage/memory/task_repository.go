// Package memory provides an in-memory implementation of task.Repository.
//
// It is intentionally small and dependency-free. The API server uses it so the
// project can run with a single `go run` command. A production deployment
// would normally replace this package with a database adapter while keeping the
// same task.Repository contract.
package memory

import (
	"context"
	"sort"
	"sync"

	"example.com/taskflow/internal/domain/task"
)

// TaskRepository stores tasks in a map protected by a read/write mutex.
//
// The zero value is not ready for use because the map must be initialized;
// always construct one with NewTaskRepository.
type TaskRepository struct {
	mu    sync.RWMutex         // Guards tasks. RWMutex allows concurrent readers.
	tasks map[string]task.Task // Primary key is Task.ID.
}

// Compile-time check that this adapter satisfies the domain contract.
// If the interface changes, the build fails here instead of at a distant call
// site, which makes refactoring much safer.
var _ task.Repository = (*TaskRepository)(nil)

// NewTaskRepository creates an empty, ready-to-use repository.
func NewTaskRepository() *TaskRepository {
	return &TaskRepository{
		tasks: make(map[string]task.Task),
	}
}

// Create stores a copy of the supplied task.
//
// Task contains only value fields (strings and time.Time), so assigning *t
// already creates an independent copy. The copy prevents a caller from
// mutating stored state through a pointer after the call returns.
func (r *TaskRepository) Create(ctx context.Context, t *task.Task) error {
	// Respect cancellation before doing any work. A real database driver does
	// the same thing internally.
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[t.ID]; exists {
		return task.ErrAlreadyExists
	}

	r.tasks[t.ID] = *t
	return nil
}

// GetByID returns a copy of the stored task.
func (r *TaskRepository) GetByID(ctx context.Context, id string) (task.Task, error) {
	if err := ctx.Err(); err != nil {
		return task.Task{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	t, exists := r.tasks[id]
	if !exists {
		return task.Task{}, task.ErrNotFound
	}
	return t, nil
}

// List returns a filtered, sorted, and paginated snapshot of the store.
//
// The map is copied while holding the read lock, then sorted after releasing
// it. Sorting a private slice avoids blocking writers for longer than needed.
func (r *TaskRepository) List(ctx context.Context, filter task.Filter) ([]task.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	tasks := make([]task.Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.Priority != "" && t.Priority != filter.Priority {
			continue
		}
		tasks = append(tasks, t)
	}
	r.mu.RUnlock()

	// Stable ordering is part of the repository contract. Newest tasks first,
	// with ID as a deterministic tie-breaker for identical timestamps.
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	// Pagination is applied after filtering and sorting. Clamp user-supplied
	// values so a negative offset cannot cause a slice-bounds panic, and min()
	// handles an offset beyond the end of the result set.
	offset := max(filter.Offset, 0)
	start := min(offset, len(tasks))
	end := len(tasks)
	if filter.Limit > 0 {
		end = min(start+filter.Limit, len(tasks))
	}
	return tasks[start:end], nil
}

// Update replaces an existing task with a copy of the supplied value.
func (r *TaskRepository) Update(ctx context.Context, t *task.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[t.ID]; !exists {
		return task.ErrNotFound
	}

	r.tasks[t.ID] = *t
	return nil
}

// Delete removes a task by ID.
func (r *TaskRepository) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[id]; !exists {
		return task.ErrNotFound
	}

	delete(r.tasks, id)
	return nil
}
