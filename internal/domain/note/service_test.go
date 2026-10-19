package note

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeRepository is a small in-memory note.Repository for domain tests.
type fakeRepository struct {
	notes map[string]Note
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{notes: make(map[string]Note)}
}

func (f *fakeRepository) Create(_ context.Context, value *Note) error {
	if _, exists := f.notes[value.ID]; exists {
		return ErrAlreadyExists
	}
	f.notes[value.ID] = *value
	return nil
}

func (f *fakeRepository) GetByID(_ context.Context, id string) (Note, error) {
	value, exists := f.notes[id]
	if !exists {
		return Note{}, ErrNotFound
	}
	return value, nil
}

func (f *fakeRepository) List(_ context.Context, filter Filter) ([]Note, error) {
	values := make([]Note, 0, len(f.notes))
	for _, value := range f.notes {
		if filter.TaskID != "" && value.TaskID != filter.TaskID {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i].CreatedAt.After(values[j].CreatedAt)
	})

	start := min(max(filter.Offset, 0), len(values))
	end := len(values)
	if filter.Limit > 0 {
		end = min(start+filter.Limit, len(values))
	}
	return values[start:end], nil
}

func (f *fakeRepository) CountByTask(_ context.Context, taskID string) (int64, error) {
	var count int64
	for _, value := range f.notes {
		if value.TaskID == taskID {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepository) Update(_ context.Context, value *Note) error {
	if _, exists := f.notes[value.ID]; !exists {
		return ErrNotFound
	}
	f.notes[value.ID] = *value
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id string) error {
	if _, exists := f.notes[id]; !exists {
		return ErrNotFound
	}
	delete(f.notes, id)
	return nil
}

func newDeterministicService(repo Repository, now time.Time, id string) *Service {
	service := NewService(repo)
	service.now = func() time.Time { return now }
	service.newID = func() string { return id }
	return service
}

func TestCreateAppliesDefaultsAndNormalizesTags(t *testing.T) {
	repo := newFakeRepository()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	service := newDeterministicService(repo, now, "note-1")

	created, err := service.Create(context.Background(), CreateInput{
		Body: "  Follow up  ",
		Tags: []string{" client ", "client", "", "follow-up"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.Body != "Follow up" {
		t.Errorf("Body = %q, want trimmed text", created.Body)
	}
	if created.Author != "anonymous" {
		t.Errorf("Author = %q, want anonymous", created.Author)
	}
	if len(created.Tags) != 2 || created.Tags[0] != "client" || created.Tags[1] != "follow-up" {
		t.Errorf("Tags = %#v, want de-duplicated [client follow-up]", created.Tags)
	}
	if !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
		t.Errorf("timestamps = %v / %v, want %v", created.CreatedAt, created.UpdatedAt, now)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     CreateInput
		wantField string
	}{
		{
			name:      "empty body",
			input:     CreateInput{Body: "   "},
			wantField: "body",
		},
		{
			name:      "body too long",
			input:     CreateInput{Body: strings.Repeat("a", maxBodyLength+1)},
			wantField: "body",
		},
		{
			name:      "invalid task id",
			input:     CreateInput{Body: "valid", TaskID: "not-a-uuid"},
			wantField: "task_id",
		},
		{
			name: "too many tags",
			input: CreateInput{Body: "valid", Tags: func() []string {
				tags := make([]string, maxTags+1)
				for index := range tags {
					tags[index] = "tag-" + string(rune('a'+index))
				}
				return tags
			}()},
			wantField: "tags",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newFakeRepository()
			service := newDeterministicService(repo, time.Now(), "note-1")

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
			if len(repo.notes) != 0 {
				t.Errorf("repository contains %d notes, want 0", len(repo.notes))
			}
		})
	}
}

func TestUpdateAppliesPartialChanges(t *testing.T) {
	repo := newFakeRepository()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	service := newDeterministicService(repo, now, "note-1")

	created, err := service.Create(context.Background(), CreateInput{
		TaskID: "4f4c0f4e-6b1a-4e2f-9c3d-2a1b0c9d8e7f",
		Author: "yotech",
		Body:   "Original",
		Tags:   []string{"one"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now = now.Add(time.Hour)
	service.now = func() time.Time { return now }
	newBody := "Updated"
	updated, err := service.Update(context.Background(), created.ID, UpdateInput{Body: &newBody})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Body != newBody {
		t.Errorf("Body = %q, want %q", updated.Body, newBody)
	}
	if updated.Author != "yotech" {
		t.Errorf("Author = %q, want unchanged", updated.Author)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "one" {
		t.Errorf("Tags = %#v, want unchanged", updated.Tags)
	}
	if !updated.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", updated.UpdatedAt, now)
	}
}

func TestUpdateRejectsEmptyPatch(t *testing.T) {
	repo := newFakeRepository()
	service := newDeterministicService(repo, time.Now(), "note-1")

	_, err := service.Update(context.Background(), "note-1", UpdateInput{})
	if err == nil {
		t.Fatal("Update() error = nil, want a validation error")
	}
}

func TestListValidatesFilter(t *testing.T) {
	repo := newFakeRepository()
	service := newDeterministicService(repo, time.Now(), "note-1")

	tests := []struct {
		name      string
		filter    Filter
		wantField string
	}{
		{"invalid task id", Filter{TaskID: "nope"}, "task_id"},
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

func TestCountByTask(t *testing.T) {
	repo := newFakeRepository()
	service := newDeterministicService(repo, time.Now(), "note-1")
	taskID := "4f4c0f4e-6b1a-4e2f-9c3d-2a1b0c9d8e7f"

	for index := 0; index < 2; index++ {
		id := "note-" + string(rune('a'+index))
		service.newID = func() string { return id }
		if _, err := service.Create(context.Background(), CreateInput{TaskID: taskID, Body: "body"}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	count, err := service.CountByTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("CountByTask() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("CountByTask = %d, want 2", count)
	}
}
