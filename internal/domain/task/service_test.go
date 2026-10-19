package task

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeRepository is a hand-written test double for Repository.
//
// It is intentionally simple: a map with the same core semantics as the real
// in-memory adapter. Using a fake (instead of a mocking library) keeps the test
// readable and demonstrates that any type implementing the interface works.
type fakeRepository struct {
	tasks map[string]Task
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[string]Task)}
}

func (f *fakeRepository) Create(_ context.Context, t *Task) error {
	if _, exists := f.tasks[t.ID]; exists {
		return ErrAlreadyExists
	}
	f.tasks[t.ID] = *t
	return nil
}

func (f *fakeRepository) GetByID(_ context.Context, id string) (Task, error) {
	t, exists := f.tasks[id]
	if !exists {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (f *fakeRepository) List(_ context.Context, filter Filter) ([]Task, error) {
	tasks := make([]Task, 0, len(f.tasks))
	for _, t := range f.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		if filter.Priority != "" && t.Priority != filter.Priority {
			continue
		}
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	start := min(max(filter.Offset, 0), len(tasks))
	end := len(tasks)
	if filter.Limit > 0 {
		end = min(start+filter.Limit, len(tasks))
	}
	return tasks[start:end], nil
}

func (f *fakeRepository) Update(_ context.Context, t *Task) error {
	if _, exists := f.tasks[t.ID]; !exists {
		return ErrNotFound
	}
	f.tasks[t.ID] = *t
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id string) error {
	if _, exists := f.tasks[id]; !exists {
		return ErrNotFound
	}
	delete(f.tasks, id)
	return nil
}

// newDeterministicService returns a service whose clock and IDs are stable so
// assertions can compare exact values.
func newDeterministicService(repository Repository, now time.Time, id string) *Service {
	service := NewService(repository)
	service.now = func() time.Time { return now }
	service.newID = func() string { return id }
	return service
}

func TestServiceCreateAppliesDefaults(t *testing.T) {
	repository := newFakeRepository()
	createdAt := time.Date(2026, time.January, 15, 10, 30, 0, 0, time.UTC)
	service := newDeterministicService(repository, createdAt, "task-1")

	created, err := service.Create(context.Background(), CreateInput{
		Title:       "  Learn Go  ",
		Description: "  Read the standard library docs.  ",
	})
	if err != nil {
		t.Fatalf("Create() returned an unexpected error: %v", err)
	}

	// Surrounding whitespace should be removed before storage.
	if created.Title != "Learn Go" {
		t.Errorf("Title = %q, want %q", created.Title, "Learn Go")
	}
	if created.Description != "Read the standard library docs." {
		t.Errorf("Description = %q, want trimmed text", created.Description)
	}

	// Defaults are domain rules, not HTTP rules.
	if created.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", created.Status, StatusTodo)
	}
	if created.Priority != PriorityMedium {
		t.Errorf("Priority = %q, want %q", created.Priority, PriorityMedium)
	}

	if created.ID != "task-1" {
		t.Errorf("ID = %q, want %q", created.ID, "task-1")
	}
	if !created.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v, want %v", created.CreatedAt, createdAt)
	}
	if !created.UpdatedAt.Equal(createdAt) {
		t.Errorf("UpdatedAt = %v, want %v", created.UpdatedAt, createdAt)
	}

	// The repository should contain exactly one stored task.
	if len(repository.tasks) != 1 {
		t.Fatalf("repository contains %d tasks, want 1", len(repository.tasks))
	}
}

func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     CreateInput
		wantField string
	}{
		{
			name:      "empty title",
			input:     CreateInput{Title: "   "},
			wantField: "title",
		},
		{
			name:      "title too long",
			input:     CreateInput{Title: strings.Repeat("a", maxTitleLength+1)},
			wantField: "title",
		},
		{
			name:      "description too long",
			input:     CreateInput{Title: "Valid title", Description: strings.Repeat("a", maxDescriptionLength+1)},
			wantField: "description",
		},
		{
			name:      "unsupported priority",
			input:     CreateInput{Title: "Valid title", Priority: Priority("urgent")},
			wantField: "priority",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newFakeRepository()
			service := newDeterministicService(repository, time.Now(), "task-1")

			_, err := service.Create(context.Background(), test.input)
			if err == nil {
				t.Fatal("Create() error = nil, want a validation error")
			}

			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if validationError.Field != test.wantField {
				t.Errorf("validation field = %q, want %q", validationError.Field, test.wantField)
			}

			// Invalid input must never reach the repository.
			if len(repository.tasks) != 0 {
				t.Errorf("repository contains %d tasks, want 0", len(repository.tasks))
			}
		})
	}
}

func TestServiceUpdateAppliesPartialChanges(t *testing.T) {
	repository := newFakeRepository()
	now := time.Date(2026, time.February, 1, 9, 0, 0, 0, time.UTC)

	// Build the service here instead of using the helper because this test
	// advances the clock between Create and Update. The closure captures the
	// variable, so reassigning `now` changes what the service sees.
	service := NewService(repository)
	service.newID = func() string { return "task-1" }
	service.now = func() time.Time { return now }

	created, err := service.Create(context.Background(), CreateInput{Title: "Original"})
	if err != nil {
		t.Fatalf("Create() returned an unexpected error: %v", err)
	}

	now = now.Add(time.Hour)
	newTitle := "Updated title"
	newStatus := StatusInProgress
	updated, err := service.Update(context.Background(), created.ID, UpdateInput{
		Title:  &newTitle,
		Status: &newStatus,
	})
	if err != nil {
		t.Fatalf("Update() returned an unexpected error: %v", err)
	}

	if updated.Title != newTitle {
		t.Errorf("Title = %q, want %q", updated.Title, newTitle)
	}
	if updated.Status != newStatus {
		t.Errorf("Status = %q, want %q", updated.Status, newStatus)
	}
	if !updated.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", updated.UpdatedAt, now)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt changed during update: got %v, want %v", updated.CreatedAt, created.CreatedAt)
	}
	// Fields not included in the patch must remain untouched.
	if updated.Priority != PriorityMedium {
		t.Errorf("Priority = %q, want unchanged %q", updated.Priority, PriorityMedium)
	}
}

func TestServiceUpdateRejectsEmptyPatch(t *testing.T) {
	repository := newFakeRepository()
	service := newDeterministicService(repository, time.Now(), "task-1")

	created, err := service.Create(context.Background(), CreateInput{Title: "Original"})
	if err != nil {
		t.Fatalf("Create() returned an unexpected error: %v", err)
	}

	_, err = service.Update(context.Background(), created.ID, UpdateInput{})
	if err == nil {
		t.Fatal("Update() error = nil, want a validation error")
	}

	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if validationError.Field != "body" {
		t.Errorf("validation field = %q, want %q", validationError.Field, "body")
	}
}

func TestServiceUpdateMissingTask(t *testing.T) {
	repository := newFakeRepository()
	service := newDeterministicService(repository, time.Now(), "task-1")

	title := "Does not matter"
	_, err := service.Update(context.Background(), "missing", UpdateInput{Title: &title})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestServiceListRejectsInvalidFilter(t *testing.T) {
	repository := newFakeRepository()
	service := newDeterministicService(repository, time.Now(), "task-1")

	tests := []struct {
		name      string
		filter    Filter
		wantField string
	}{
		{"invalid status", Filter{Status: Status("archived")}, "status"},
		{"invalid priority", Filter{Priority: Priority("urgent")}, "priority"},
		{"negative offset", Filter{Offset: -1}, "offset"},
		{"negative limit", Filter{Limit: -1}, "limit"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.List(context.Background(), test.filter)

			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want *ValidationError", err)
			}
			if validationError.Field != test.wantField {
				t.Errorf("validation field = %q, want %q", validationError.Field, test.wantField)
			}
		})
	}
}
