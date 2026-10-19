package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/taskflow/internal/domain/task"
)

// TestTaskRepositoryStoresCopies verifies that callers cannot mutate stored
// state through pointers or returned values.
func TestTaskRepositoryStoresCopies(t *testing.T) {
	repository := NewTaskRepository()
	ctx := context.Background()

	original := task.Task{
		ID:        "task-1",
		Title:     "Original",
		Status:    task.StatusTodo,
		Priority:  task.PriorityMedium,
		CreatedAt: time.Now().UTC(),
	}
	if err := repository.Create(ctx, &original); err != nil {
		t.Fatalf("Create() returned an unexpected error: %v", err)
	}

	// Mutating the caller's value must not change the stored task.
	original.Title = "Mutated after create"

	stored, err := repository.GetByID(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetByID() returned an unexpected error: %v", err)
	}
	if stored.Title != "Original" {
		t.Errorf("stored Title = %q, want %q", stored.Title, "Original")
	}

	// Mutating the returned value must not change the stored task either.
	stored.Title = "Mutated after get"
	again, err := repository.GetByID(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetByID() returned an unexpected error: %v", err)
	}
	if again.Title != "Original" {
		t.Errorf("stored Title after second get = %q, want %q", again.Title, "Original")
	}
}

// TestTaskRepositoryListFiltersSortsAndPaginates covers the query behavior the
// HTTP list endpoint relies on.
func TestTaskRepositoryListFiltersSortsAndPaginates(t *testing.T) {
	repository := NewTaskRepository()
	ctx := context.Background()

	base := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	tasks := []task.Task{
		{ID: "old-todo", Title: "Old todo", Status: task.StatusTodo, Priority: task.PriorityLow, CreatedAt: base},
		{ID: "new-todo", Title: "New todo", Status: task.StatusTodo, Priority: task.PriorityHigh, CreatedAt: base.Add(2 * time.Hour)},
		{ID: "middle-todo", Title: "Middle todo", Status: task.StatusTodo, Priority: task.PriorityMedium, CreatedAt: base.Add(time.Hour)},
		{ID: "done", Title: "Done task", Status: task.StatusDone, Priority: task.PriorityHigh, CreatedAt: base.Add(3 * time.Hour)},
	}
	for i := range tasks {
		if err := repository.Create(ctx, &tasks[i]); err != nil {
			t.Fatalf("Create(%q) returned an unexpected error: %v", tasks[i].ID, err)
		}
	}

	// Filter by status, then verify newest-first ordering.
	todo, err := repository.List(ctx, task.Filter{Status: task.StatusTodo})
	if err != nil {
		t.Fatalf("List() returned an unexpected error: %v", err)
	}
	if len(todo) != 3 {
		t.Fatalf("List() returned %d tasks, want 3", len(todo))
	}
	wantOrder := []string{"new-todo", "middle-todo", "old-todo"}
	for i, wantID := range wantOrder {
		if todo[i].ID != wantID {
			t.Errorf("todo[%d].ID = %q, want %q", i, todo[i].ID, wantID)
		}
	}

	// Filter by priority as well as status.
	highTodos, err := repository.List(ctx, task.Filter{
		Status:   task.StatusTodo,
		Priority: task.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("List() returned an unexpected error: %v", err)
	}
	if len(highTodos) != 1 || highTodos[0].ID != "new-todo" {
		t.Errorf("high-priority todos = %+v, want only new-todo", highTodos)
	}

	// Pagination applies after sorting: offset 1, limit 1 is the middle task.
	page, err := repository.List(ctx, task.Filter{Status: task.StatusTodo, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("List() returned an unexpected error: %v", err)
	}
	if len(page) != 1 || page[0].ID != "middle-todo" {
		t.Errorf("paginated result = %+v, want [middle-todo]", page)
	}
}

// TestTaskRepositoryMissingTask documents the sentinel errors callers can rely
// on with errors.Is.
func TestTaskRepositoryMissingTask(t *testing.T) {
	repository := NewTaskRepository()
	ctx := context.Background()

	if _, err := repository.GetByID(ctx, "missing"); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("GetByID() error = %v, want ErrNotFound", err)
	}
	if err := repository.Update(ctx, &task.Task{ID: "missing"}); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
	if err := repository.Delete(ctx, "missing"); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}
