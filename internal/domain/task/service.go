package task

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Validation limits are shared by create and update operations so both paths
// enforce exactly the same rules.
const (
	maxTitleLength       = 200
	maxDescriptionLength = 2000

	// DefaultListLimit is used when the caller does not request a page size.
	DefaultListLimit = 20

	// MaxListLimit protects the server from unexpectedly expensive queries.
	// Larger requested values are clamped rather than rejected, which is a
	// common and forgiving API design.
	MaxListLimit = 100
)

// Service implements the application's task use cases.
//
// It coordinates validation, defaults, ID generation, timestamps, the
// repository, the optional Redis cache, and the optional MongoDB audit log. It
// does not know how data is serialized or where it is stored; those details are
// behind small interfaces.
type Service struct {
	repo   Repository
	cache  TaskCache     // Optional: nil disables read-through caching.
	events EventRecorder // Optional: nil disables the MongoDB audit trail.
	logger *slog.Logger

	// now and newID are function fields so tests can make time and IDs
	// deterministic. In production they point at time.Now and uuid.NewString.
	now   func() time.Time
	newID func() string
}

// Option customizes a Service without making the constructor signature grow
// forever. Functional options are a common Go pattern for optional
// dependencies.
type Option func(*Service)

// WithCache enables read-through caching and view counters.
func WithCache(cache TaskCache) Option {
	return func(service *Service) {
		service.cache = cache
	}
}

// WithEventRecorder enables the MongoDB audit trail.
func WithEventRecorder(recorder EventRecorder) Option {
	return func(service *Service) {
		service.events = recorder
	}
}

// WithLogger injects the structured logger used for service-level traces.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// NewService wires the required repository and any optional collaborators.
//
// A nil repository is a programming error (not user input), so failing fast
// with a panic at startup is preferable to silently accepting every request
// and failing later.
func NewService(repo Repository, options ...Option) *Service {
	if repo == nil {
		panic("task.NewService: repository must not be nil")
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

// Create validates the input, applies defaults, and stores a new task.
//
// Ordering is deliberate:
//  1. validate everything before touching a database;
//  2. write PostgreSQL (the source of truth);
//  3. update Redis best-effort;
//  4. append an audit event to MongoDB best-effort.
//
// Steps 3 and 4 never fail the request. A cache outage or audit-log outage must
// not lose a task that PostgreSQL has already committed. Both failures are
// logged at error level so they are visible in operations.
func (s *Service) Create(ctx context.Context, input CreateInput) (Task, error) {
	s.logger.DebugContext(ctx, "task.create: validating input",
		"title_length", utf8.RuneCountInString(input.Title),
		"priority", input.Priority,
	)

	title, err := normalizeTitle(input.Title)
	if err != nil {
		return Task{}, err
	}

	description, err := normalizeDescription(input.Description)
	if err != nil {
		return Task{}, err
	}

	// Priority is optional at the API boundary. The domain chooses the
	// default so every client gets consistent behavior.
	priority := input.Priority
	if priority == "" {
		priority = PriorityMedium
	}
	if !priority.Valid() {
		return Task{}, &ValidationError{
			Field:   "priority",
			Message: fmt.Sprintf("must be one of %q, %q, or %q", PriorityLow, PriorityMedium, PriorityHigh),
		}
	}

	// Work with UTC everywhere. It avoids ambiguity around daylight-saving
	// transitions and makes timestamps comparable across servers.
	now := s.now().UTC()
	created := Task{
		ID:          s.newID(),
		Title:       title,
		Description: description,
		Status:      StatusTodo, // New tasks always start in the backlog.
		Priority:    priority,
		DueDate:     utcPointer(input.DueDate),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.logger.DebugContext(ctx, "task.create: writing PostgreSQL row", "task_id", created.ID)
	if err := s.repo.Create(ctx, &created); err != nil {
		s.logger.ErrorContext(ctx, "task.create: repository write failed",
			"task_id", created.ID, "error", err)
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	s.cacheTask(ctx, created)
	s.recordEvent(ctx, EventTaskCreated, created)

	s.logger.InfoContext(ctx, "task.create: task created",
		"task_id", created.ID,
		"status", created.Status,
		"priority", created.Priority,
		"due_date", created.DueDate,
	)
	return created, nil
}

// Get returns a single task by ID using read-through caching.
//
// A cache failure is never fatal: the method logs it and falls back to
// PostgreSQL. Cached values are stored as JSON, so upgrading the Task struct is
// safe as long as new fields are optional.
func (s *Service) Get(ctx context.Context, id string) (Task, error) {
	if s.cache != nil {
		cached, found, err := s.cache.GetTask(ctx, id)
		if err != nil {
			s.logger.ErrorContext(ctx, "task.get: Redis read failed; falling back to PostgreSQL",
				"task_id", id, "error", err)
		} else if found {
			s.logger.InfoContext(ctx, "task.get: cache hit", "task_id", id)
			return cached, nil
		} else {
			s.logger.DebugContext(ctx, "task.get: cache miss", "task_id", id)
		}
	}

	s.logger.DebugContext(ctx, "task.get: reading PostgreSQL", "task_id", id)
	found, err := s.repo.GetByID(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "task.get: task not available",
			"task_id", id, "error", err)
		return Task{}, fmt.Errorf("get task %q: %w", id, err)
	}

	s.cacheTask(ctx, found)
	return found, nil
}

// List returns tasks matching the supplied filter.
//
// The method validates user-controlled filter values, applies the default page
// size, and clamps oversized pages. Listing intentionally bypasses Redis: a
// filtered, paginated query has too many cache-key combinations to be worth it
// in this sample. The cache is used for single-task reads and the overview.
func (s *Service) List(ctx context.Context, filter Filter) ([]Task, error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, &ValidationError{Field: "status", Message: "is not a supported status"}
	}
	if filter.Priority != "" && !filter.Priority.Valid() {
		return nil, &ValidationError{Field: "priority", Message: "is not a supported priority"}
	}
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

	s.logger.DebugContext(ctx, "task.list: querying PostgreSQL",
		"status", filter.Status,
		"priority", filter.Priority,
		"limit", filter.Limit,
		"offset", filter.Offset,
	)
	tasks, err := s.repo.List(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "task.list: query failed", "error", err)
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	// A nil slice marshals to JSON null. Force an empty slice so clients always
	// receive a predictable JSON array.
	if tasks == nil {
		tasks = []Task{}
	}
	s.logger.InfoContext(ctx, "task.list: query succeeded", "count", len(tasks))
	return tasks, nil
}

// Update applies a partial update to an existing task.
//
// The service fetches the current value, changes only the supplied fields,
// validates the result, and writes it back. A production system with multiple
// writers would usually add optimistic concurrency control (for example a
// version column) to prevent lost updates.
func (s *Service) Update(ctx context.Context, id string, input UpdateInput) (Task, error) {
	// Refuse an entirely empty patch instead of silently touching UpdatedAt.
	if input.Title == nil && input.Description == nil &&
		input.Status == nil && input.Priority == nil &&
		input.DueDate == nil && !input.ClearDueDate {
		return Task{}, &ValidationError{
			Field:   "body",
			Message: "at least one of title, description, status, priority, or due_date is required",
		}
	}

	s.logger.DebugContext(ctx, "task.update: loading current task", "task_id", id)
	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("get task %q for update: %w", id, err)
	}

	if input.Title != nil {
		current.Title, err = normalizeTitle(*input.Title)
		if err != nil {
			return Task{}, err
		}
	}

	if input.Description != nil {
		current.Description, err = normalizeDescription(*input.Description)
		if err != nil {
			return Task{}, err
		}
	}

	if input.Status != nil {
		if !input.Status.Valid() {
			return Task{}, &ValidationError{Field: "status", Message: "is not a supported status"}
		}
		current.Status = *input.Status
	}

	if input.Priority != nil {
		if !input.Priority.Valid() {
			return Task{}, &ValidationError{Field: "priority", Message: "is not a supported priority"}
		}
		current.Priority = *input.Priority
	}

	if input.ClearDueDate {
		current.DueDate = nil
	} else if input.DueDate != nil {
		current.DueDate = utcPointer(input.DueDate)
	}

	current.UpdatedAt = s.now().UTC()

	s.logger.DebugContext(ctx, "task.update: writing PostgreSQL row", "task_id", id)
	if err := s.repo.Update(ctx, &current); err != nil {
		s.logger.ErrorContext(ctx, "task.update: repository write failed",
			"task_id", id, "error", err)
		return Task{}, fmt.Errorf("update task %q: %w", id, err)
	}

	// Refresh the cache instead of deleting it. The write is cheap and the next
	// read is a guaranteed hit; a failure is still non-fatal.
	s.cacheTask(ctx, current)
	s.recordEvent(ctx, EventTaskUpdated, current)

	s.logger.InfoContext(ctx, "task.update: task updated",
		"task_id", id, "status", current.Status, "priority", current.Priority)
	return current, nil
}

// Delete removes a task by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.logger.DebugContext(ctx, "task.delete: deleting PostgreSQL row", "task_id", id)
	if err := s.repo.Delete(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "task.delete: repository delete failed",
			"task_id", id, "error", err)
		return fmt.Errorf("delete task %q: %w", id, err)
	}

	if s.cache != nil {
		if err := s.cache.DeleteTask(ctx, id); err != nil {
			s.logger.ErrorContext(ctx, "task.delete: cache invalidation failed",
				"task_id", id, "error", err)
		}
	}

	// The task no longer exists, so the event carries only its identifier and
	// a zero-value snapshot. The event type and timestamp still tell the story.
	deleted := Task{ID: id, UpdatedAt: s.now().UTC()}
	s.recordEvent(ctx, EventTaskDeleted, deleted)

	s.logger.InfoContext(ctx, "task.delete: task deleted", "task_id", id)
	return nil
}

// cacheTask writes a task to Redis and logs (but does not return) failures.
func (s *Service) cacheTask(ctx context.Context, value Task) {
	if s.cache == nil {
		return
	}
	if err := s.cache.SetTask(ctx, value); err != nil {
		s.logger.ErrorContext(ctx, "cache write failed",
			"task_id", value.ID, "error", err)
		return
	}
	s.logger.DebugContext(ctx, "cache write succeeded", "task_id", value.ID)
}

// recordEvent appends an audit document to MongoDB and logs failures.
func (s *Service) recordEvent(ctx context.Context, eventType TaskEventType, snapshot Task) {
	if s.events == nil {
		return
	}

	event := TaskEvent{
		ID:         uuid.NewString(),
		TaskID:     snapshot.ID,
		Type:       eventType,
		Snapshot:   snapshot,
		OccurredAt: s.now().UTC(),
	}
	if err := s.events.RecordTaskEvent(ctx, event); err != nil {
		s.logger.ErrorContext(ctx, "audit event write failed",
			"event_type", eventType,
			"task_id", snapshot.ID,
			"error", err,
		)
		return
	}
	s.logger.DebugContext(ctx, "audit event recorded",
		"event_type", eventType, "task_id", snapshot.ID)
}

// utcPointer normalizes an optional timestamp to UTC. Returning a new pointer
// avoids mutating the caller's value.
func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// normalizeTitle trims surrounding whitespace and validates length.
// Returning the normalized value keeps storage consistent regardless of which
// transport created the task.
func normalizeTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", &ValidationError{Field: "title", Message: "must not be empty"}
	}
	if utf8.RuneCountInString(title) > maxTitleLength {
		return "", &ValidationError{
			Field:   "title",
			Message: fmt.Sprintf("must be at most %d characters", maxTitleLength),
		}
	}
	return title, nil
}

// normalizeDescription trims surrounding whitespace and validates length.
// An empty description is allowed.
func normalizeDescription(raw string) (string, error) {
	description := strings.TrimSpace(raw)
	if utf8.RuneCountInString(description) > maxDescriptionLength {
		return "", &ValidationError{
			Field:   "description",
			Message: fmt.Sprintf("must be at most %d characters", maxDescriptionLength),
		}
	}
	return description, nil
}
