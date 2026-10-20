// Package memory provides an in-memory implementation of task.Repository.
//
// It is intentionally small and dependency-free. The HTTP test suite uses it
// so the API can be exercised without a database, and it doubles as the
// reference implementation of the task.Repository contract: the PostgreSQL
// adapter implements exactly the same interface.
package memory

import (
	"context"
	"errors"
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
// The copy is deep. task.Task carries a *time.Time for the optional due date,
// and a shallow copy would leave the stored task and the caller's task
// pointing at the same deadline, so the caller could change stored state
// after the call returns.
func (r *TaskRepository) Create(ctx context.Context, t *task.Task) error {
	// Respect cancellation before doing any work. A real database driver does
	// the same thing internally.
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil {
		return errors.New("memory: task must not be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[t.ID]; exists {
		return task.ErrAlreadyExists
	}

	r.tasks[t.ID] = cloneTask(*t)
	return nil
}

// GetByID returns a copy of the stored task.
//
// The returned task shares no mutable state with the stored one, so writing
// through the returned DueDate pointer cannot corrupt the store.
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
	return cloneTask(t), nil
}

// List returns a filtered, sorted, and paginated snapshot of the store.
//
// The map is copied while holding the read lock, then sorted after releasing
// it. Sorting a private slice avoids blocking writers for longer than needed.
func (r *TaskRepository) List(ctx context.Context, filter task.Filter) ([]task.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tasks := r.snapshot(filter)

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

// snapshot copies the matching tasks while holding the read lock.
//
// The lock is released with defer, so a panic inside the copy cannot leave the
// mutex locked forever and block every later request.
func (r *TaskRepository) snapshot(filter task.Filter) []task.Task {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]task.Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.Priority != "" && t.Priority != filter.Priority {
			continue
		}
		tasks = append(tasks, cloneTask(t))
	}
	return tasks
}

// Update replaces an existing task with a copy of the supplied value.
func (r *TaskRepository) Update(ctx context.Context, t *task.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil {
		return errors.New("memory: task must not be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tasks[t.ID]; !exists {
		return task.ErrNotFound
	}

	r.tasks[t.ID] = cloneTask(*t)
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

// cloneTask returns a copy of value that shares no mutable state with it.
//
// task.Task has exactly one pointer field, DueDate. Copying the struct alone
// would leave both copies pointing at the same time.Time, so a caller could
// change the stored deadline by writing through the pointer.
func cloneTask(value task.Task) task.Task {
	clone := value
	if value.DueDate != nil {
		due := *value.DueDate
		clone.DueDate = &due
	}
	return clone
}
