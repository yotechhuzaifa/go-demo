package note

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Validation limits are shared by every write path.
const (
	maxAuthorLength = 100
	maxBodyLength   = 5000
	maxTags         = 10
	maxTagLength    = 40

	// DefaultListLimit is used when no page size is requested.
	DefaultListLimit = 20
	// MaxListLimit caps expensive queries.
	MaxListLimit = 100
)

// Service implements the note use cases.
type Service struct {
	repo   Repository
	logger *slog.Logger

	now   func() time.Time
	newID func() string
}

// Option customizes a Service.
type Option func(*Service)

// WithLogger injects the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// NewService wires a repository into the domain service.
func NewService(repo Repository, options ...Option) *Service {
	if repo == nil {
		panic("note.NewService: repository must not be nil")
	}

	service := &Service{
		repo:   repo,
		logger: slog.Default(),
		now:    time.Now,
		newID:  uuid.NewString,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Create validates and stores a note.
func (s *Service) Create(ctx context.Context, input CreateInput) (Note, error) {
	s.logger.DebugContext(ctx, "note.create: validating input",
		"task_id", input.TaskID,
		"body_length", utf8.RuneCountInString(input.Body),
	)

	taskID, err := normalizeOptionalTaskID(input.TaskID)
	if err != nil {
		return Note{}, err
	}

	author, err := normalizeAuthor(input.Author)
	if err != nil {
		return Note{}, err
	}

	body, err := normalizeBody(input.Body)
	if err != nil {
		return Note{}, err
	}

	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Note{}, err
	}

	now := s.now().UTC()
	created := Note{
		ID:        s.newID(),
		TaskID:    taskID,
		Author:    author,
		Body:      body,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.logger.DebugContext(ctx, "note.create: inserting MongoDB document", "note_id", created.ID)
	if err := s.repo.Create(ctx, &created); err != nil {
		s.logger.ErrorContext(ctx, "note.create: MongoDB insert failed",
			"note_id", created.ID, "error", err)
		return Note{}, fmt.Errorf("create note: %w", err)
	}

	s.logger.InfoContext(ctx, "note.create: note created",
		"note_id", created.ID, "task_id", created.TaskID, "author", created.Author)
	return created, nil
}

// Get returns a note by ID.
func (s *Service) Get(ctx context.Context, id string) (Note, error) {
	s.logger.DebugContext(ctx, "note.get: querying MongoDB", "note_id", id)
	found, err := s.repo.GetByID(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "note.get: note not available", "note_id", id, "error", err)
		return Note{}, fmt.Errorf("get note %q: %w", id, err)
	}
	s.logger.InfoContext(ctx, "note.get: note found", "note_id", id)
	return found, nil
}

// List returns notes matching the filter.
func (s *Service) List(ctx context.Context, filter Filter) ([]Note, error) {
	if filter.Offset < 0 {
		return nil, &ValidationError{Field: "offset", Message: "must be zero or greater"}
	}
	if filter.Limit < 0 {
		return nil, &ValidationError{Field: "limit", Message: "must be zero or greater"}
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultListLimit
	}
	if filter.Limit > MaxListLimit {
		filter.Limit = MaxListLimit
	}
	if filter.TaskID != "" {
		if _, err := uuid.Parse(filter.TaskID); err != nil {
			return nil, &ValidationError{Field: "task_id", Message: "must be a UUID"}
		}
	}

	s.logger.DebugContext(ctx, "note.list: querying MongoDB",
		"task_id", filter.TaskID, "limit", filter.Limit, "offset", filter.Offset)
	notes, err := s.repo.List(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "note.list: query failed", "error", err)
		return nil, fmt.Errorf("list notes: %w", err)
	}
	if notes == nil {
		notes = []Note{}
	}
	s.logger.InfoContext(ctx, "note.list: query succeeded", "count", len(notes))
	return notes, nil
}

// Update applies a partial update.
func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (Note, error) {
	if input.TaskID == nil && input.Author == nil && input.Body == nil && input.Tags == nil {
		return Note{}, &ValidationError{Field: "body", Message: "at least one field is required"}
	}

	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Note{}, fmt.Errorf("get note %q for update: %w", id, err)
	}

	if input.TaskID != nil {
		current.TaskID, err = normalizeOptionalTaskID(*input.TaskID)
		if err != nil {
			return Note{}, err
		}
	}
	if input.Author != nil {
		current.Author, err = normalizeAuthor(*input.Author)
		if err != nil {
			return Note{}, err
		}
	}
	if input.Body != nil {
		current.Body, err = normalizeBody(*input.Body)
		if err != nil {
			return Note{}, err
		}
	}
	if input.Tags != nil {
		current.Tags, err = normalizeTags(*input.Tags)
		if err != nil {
			return Note{}, err
		}
	}

	current.UpdatedAt = s.now().UTC()
	s.logger.DebugContext(ctx, "note.update: updating MongoDB document", "note_id", id)
	if err := s.repo.Update(ctx, &current); err != nil {
		s.logger.ErrorContext(ctx, "note.update: MongoDB update failed", "note_id", id, "error", err)
		return Note{}, fmt.Errorf("update note %q: %w", id, err)
	}
	s.logger.InfoContext(ctx, "note.update: note updated", "note_id", id)
	return current, nil
}

// Delete removes a note by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.logger.DebugContext(ctx, "note.delete: deleting MongoDB document", "note_id", id)
	if err := s.repo.Delete(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "note.delete: MongoDB delete failed", "note_id", id, "error", err)
		return fmt.Errorf("delete note %q: %w", id, err)
	}
	s.logger.InfoContext(ctx, "note.delete: note deleted", "note_id", id)
	return nil
}

// CountByTask returns the number of notes attached to a task.
func (s *Service) CountByTask(ctx context.Context, taskID string) (int64, error) {
	count, err := s.repo.CountByTask(ctx, taskID)
	if err != nil {
		s.logger.ErrorContext(ctx, "note.count: MongoDB count failed", "task_id", taskID, "error", err)
		return 0, fmt.Errorf("count notes for task %q: %w", taskID, err)
	}
	return count, nil
}

// normalizeAuthor trims the author and applies the default.
func normalizeAuthor(raw string) (string, error) {
	author := strings.TrimSpace(raw)
	if author == "" {
		author = "anonymous"
	}
	if utf8.RuneCountInString(author) > maxAuthorLength {
		return "", &ValidationError{
			Field:   "author",
			Message: fmt.Sprintf("must be at most %d characters", maxAuthorLength),
		}
	}
	return author, nil
}

// normalizeBody requires non-empty note text.
func normalizeBody(raw string) (string, error) {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "", &ValidationError{Field: "body", Message: "must not be empty"}
	}
	if utf8.RuneCountInString(body) > maxBodyLength {
		return "", &ValidationError{
			Field:   "body",
			Message: fmt.Sprintf("must be at most %d characters", maxBodyLength),
		}
	}
	return body, nil
}

// normalizeOptionalTaskID validates an optional UUID and normalizes its case.
func normalizeOptionalTaskID(raw string) (string, error) {
	taskID := strings.TrimSpace(raw)
	if taskID == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(taskID)
	if err != nil {
		return "", &ValidationError{Field: "task_id", Message: "must be a UUID"}
	}
	return parsed.String(), nil
}

// normalizeTags trims, de-duplicates, and validates tags. The returned slice is
// always non-nil so JSON responses show [] rather than null.
func normalizeTags(raw []string) ([]string, error) {
	tags := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))

	for _, value := range raw {
		tag := strings.TrimSpace(value)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagLength {
			return nil, &ValidationError{
				Field:   "tags",
				Message: fmt.Sprintf("each tag must be at most %d characters", maxTagLength),
			}
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}

	if len(tags) > maxTags {
		return nil, &ValidationError{
			Field:   "tags",
			Message: fmt.Sprintf("must contain at most %d tags", maxTags),
		}
	}
	return tags, nil
}
