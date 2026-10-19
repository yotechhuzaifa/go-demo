package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/dates"
	"example.com/taskflow/internal/transport/httpapi/dto"
)

// taskHandler holds the dependencies needed by task endpoints.
//
// Handlers stay thin: parse transport input, validate the DTO, call the
// service, translate the result back to HTTP. Business rules belong in the
// domain package.
type taskHandler struct {
	service *task.Service
	notes   *note.Service // Optional: used only by the overview endpoint.
	cache   task.TaskCache
	logger  *slog.Logger
}

// newTaskHandler constructs the handler set used by the router.
func newTaskHandler(
	service *task.Service,
	notes *note.Service,
	cache task.TaskCache,
	logger *slog.Logger,
) *taskHandler {
	return &taskHandler{
		service: service,
		notes:   notes,
		cache:   cache,
		logger:  logger,
	}
}

// list handles GET /api/v1/tasks.
//
// @Summary List tasks
// @Description Returns newest tasks first. PostgreSQL is queried directly; the listing bypasses the cache.
// @Tags Tasks
// @Produce json
// @Param status query string false "Filter by workflow state" Enums(todo, in_progress, done)
// @Param priority query string false "Filter by priority" Enums(low, medium, high)
// @Param limit query int false "Page size (default 20, max 100)"
// @Param offset query int false "Number of matching tasks to skip"
// @Success 200 {object} dto.TaskListResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/tasks [get]
func (h *taskHandler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit, err := intQuery(r, "limit", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	offset, err := intQuery(r, "offset", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}

	filter := task.Filter{
		Status:   task.Status(query.Get("status")),
		Priority: task.Priority(query.Get("priority")),
		Limit:    limit,
		Offset:   offset,
	}

	h.logger.DebugContext(r.Context(), "handler.tasks.list: calling service",
		"request_id", requestIDFromContext(r.Context()),
		"status", filter.Status, "priority", filter.Priority,
		"limit", filter.Limit, "offset", filter.Offset,
	)

	tasks, err := h.service.List(r.Context(), filter)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	// The service applies defaults and caps the limit. Mirror those rules here
	// so the response metadata describes the query that actually ran.
	effectiveLimit := limit
	if effectiveLimit == 0 {
		effectiveLimit = task.DefaultListLimit
	}
	if effectiveLimit > task.MaxListLimit {
		effectiveLimit = task.MaxListLimit
	}

	h.logger.InfoContext(r.Context(), "handler.tasks.list: responding",
		"request_id", requestIDFromContext(r.Context()), "count", len(tasks))

	writeJSON(w, http.StatusOK, dto.TaskListResponse{
		Data: tasksToDTO(tasks, time.Now().UTC()),
		Meta: dto.TaskListMeta{
			Limit:  effectiveLimit,
			Offset: offset,
			Count:  len(tasks),
		},
	})
}

// create handles POST /api/v1/tasks.
//
// @Summary Create a task
// @Description Validates the DTO, writes PostgreSQL, caches the task in Redis, and appends an audit event to MongoDB. Cache and audit failures are logged but do not roll back the committed task.
// @Tags Tasks
// @Accept json
// @Produce json
// @Param request body dto.CreateTaskRequest true "Task to create"
// @Success 201 {object} dto.TaskResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/tasks [post]
func (h *taskHandler) create(w http.ResponseWriter, r *http.Request) {
	var request dto.CreateTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateDTO(request); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	// Flexible date parsing is a transport concern: the domain receives an
	// unambiguous UTC instant.
	dueDate, err := dates.ParseInLocation(request.DueDate, request.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "due_date: "+err.Error())
		return
	}

	h.logger.DebugContext(r.Context(), "handler.tasks.create: calling service",
		"request_id", requestIDFromContext(r.Context()),
		"title", request.Title,
	)

	created, err := h.service.Create(r.Context(), task.CreateInput{
		Title:       request.Title,
		Description: request.Description,
		Priority:    task.Priority(request.Priority),
		DueDate:     dueDate,
	})
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	// Location identifies the new resource. It is useful for clients and is
	// part of the HTTP semantics of a 201 Created response.
	w.Header().Set("Location", "/api/v1/tasks/"+created.ID)
	h.logger.InfoContext(r.Context(), "handler.tasks.create: created",
		"request_id", requestIDFromContext(r.Context()), "task_id", created.ID)

	writeJSON(w, http.StatusCreated, dto.TaskResponse{
		Data: taskToDTO(created, time.Now().UTC()),
	})
}

// get handles GET /api/v1/tasks/{id}.
//
// @Summary Get one task
// @Description Reads through Redis first, then PostgreSQL on a cache miss.
// @Tags Tasks
// @Produce json
// @Param id path string true "Task UUID"
// @Success 200 {object} dto.TaskResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/tasks/{id} [get]
func (h *taskHandler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.logger.DebugContext(r.Context(), "handler.tasks.get: calling service",
		"request_id", requestIDFromContext(r.Context()), "task_id", id)

	found, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	h.logger.InfoContext(r.Context(), "handler.tasks.get: responding",
		"request_id", requestIDFromContext(r.Context()), "task_id", id)
	writeJSON(w, http.StatusOK, dto.TaskResponse{
		Data: taskToDTO(found, time.Now().UTC()),
	})
}

// update handles PATCH /api/v1/tasks/{id}.
//
// @Summary Partially update a task
// @Description Only supplied fields change. Updating refreshes the Redis cache and appends a MongoDB audit event.
// @Tags Tasks
// @Accept json
// @Produce json
// @Param id path string true "Task UUID"
// @Param request body dto.UpdateTaskRequest true "Fields to update"
// @Success 200 {object} dto.TaskResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/tasks/{id} [patch]
func (h *taskHandler) update(w http.ResponseWriter, r *http.Request) {
	var request dto.UpdateTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateDTO(request); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	var dueDate *time.Time
	var err error
	if request.DueDate != nil && !request.ClearDueDate {
		dueDate, err = dates.ParseInLocation(*request.DueDate, request.Timezone)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "due_date: "+err.Error())
			return
		}
	}

	input := task.UpdateInput{
		Title:        request.Title,
		Description:  request.Description,
		DueDate:      dueDate,
		ClearDueDate: request.ClearDueDate,
	}
	if request.Status != nil {
		status := task.Status(*request.Status)
		input.Status = &status
	}
	if request.Priority != nil {
		priority := task.Priority(*request.Priority)
		input.Priority = &priority
	}

	h.logger.DebugContext(r.Context(), "handler.tasks.update: calling service",
		"request_id", requestIDFromContext(r.Context()), "task_id", r.PathValue("id"))

	updated, err := h.service.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	h.logger.InfoContext(r.Context(), "handler.tasks.update: updated",
		"request_id", requestIDFromContext(r.Context()),
		"task_id", updated.ID, "status", updated.Status)
	writeJSON(w, http.StatusOK, dto.TaskResponse{
		Data: taskToDTO(updated, time.Now().UTC()),
	})
}

// delete handles DELETE /api/v1/tasks/{id}.
//
// @Summary Delete a task
// @Description Deletes the PostgreSQL row, invalidates Redis, and records a MongoDB audit event.
// @Tags Tasks
// @Param id path string true "Task UUID"
// @Success 204 "Task deleted; no body is returned"
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/tasks/{id} [delete]
func (h *taskHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.logger.DebugContext(r.Context(), "handler.tasks.delete: calling service",
		"request_id", requestIDFromContext(r.Context()), "task_id", id)

	if err := h.service.Delete(r.Context(), id); err != nil {
		h.respondError(w, r, err)
		return
	}

	h.logger.InfoContext(r.Context(), "handler.tasks.delete: deleted",
		"request_id", requestIDFromContext(r.Context()), "task_id", id)

	// 204 means success with intentionally no response body.
	w.WriteHeader(http.StatusNoContent)
}

// overview handles GET /api/v1/tasks/{id}/overview.
//
// @Summary Cross-database task overview
// @Description The one endpoint that intentionally reads all three stores: the task from PostgreSQL (through Redis), notes from MongoDB, and the view counter from Redis.
// @Tags Overview
// @Produce json
// @Param id path string true "Task UUID"
// @Success 200 {object} dto.OverviewEnvelope
// @Failure 404 {object} dto.ErrorResponse
// @Failure 503 {object} dto.ErrorResponse
// @Router /api/v1/tasks/{id}/overview [get]
func (h *taskHandler) overview(w http.ResponseWriter, r *http.Request) {
	if h.notes == nil || h.cache == nil {
		// Defensive: the router only wires this route in the full application,
		// but unit tests and future compositions should get an actionable error
		// instead of a panic.
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable",
			"overview requires MongoDB notes and Redis cache to be configured")
		return
	}

	id := r.PathValue("id")
	log := h.logger.With(
		"request_id", requestIDFromContext(r.Context()),
		"task_id", id,
	)

	log.DebugContext(r.Context(), "handler.overview: step 1/4 reading task (PostgreSQL through Redis)")
	found, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	log.DebugContext(r.Context(), "handler.overview: step 2/4 counting notes in MongoDB")
	notesCount, err := h.notes.CountByTask(r.Context(), id)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	log.DebugContext(r.Context(), "handler.overview: step 3/4 reading recent notes from MongoDB")
	recentNotes, err := h.notes.List(r.Context(), note.Filter{TaskID: id, Limit: 5})
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	log.DebugContext(r.Context(), "handler.overview: step 4/4 incrementing Redis view counter")
	viewCount, err := h.cache.IncrementTaskViews(r.Context(), id)
	if err != nil {
		// The counter is supplementary. A Redis failure should not hide the
		// task and notes, so log the error and continue with 0.
		log.ErrorContext(r.Context(), "handler.overview: Redis counter failed", "error", err)
		viewCount = 0
	}

	now := time.Now().UTC()
	response := dto.OverviewResponse{
		Task:        taskToDTO(found, now),
		NotesCount:  notesCount,
		RecentNotes: notesToDTO(recentNotes),
		ViewCount:   viewCount,
		WeekLabel:   dates.WeekLabel(now),
		GeneratedAt: now.Format(time.RFC3339),
		Sources:     []string{"postgresql", "mongodb", "redis"},
	}

	log.InfoContext(r.Context(), "handler.overview: responding",
		"notes_count", notesCount, "view_count", viewCount)

	writeJSON(w, http.StatusOK, dto.OverviewEnvelope{Data: response})
}

// respondError maps domain errors to HTTP status codes and a stable JSON shape.
//
// This is the boundary where internal details stop. Unexpected errors are
// logged with context but reported generically to clients so implementation
// details are not leaked.
func (h *taskHandler) respondError(w http.ResponseWriter, r *http.Request, err error) {
	var taskValidation *task.ValidationError
	var noteValidation *note.ValidationError

	switch {
	case errors.As(err, &taskValidation):
		writeError(w, http.StatusBadRequest, "validation_error", taskValidation.Error())

	case errors.As(err, &noteValidation):
		writeError(w, http.StatusBadRequest, "validation_error", noteValidation.Error())

	case errors.Is(err, task.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "the requested task does not exist")

	case errors.Is(err, note.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "the requested note does not exist")

	case errors.Is(err, task.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", "a task with that identifier already exists")

	case errors.Is(err, context.Canceled):
		// The client disconnected. There is no one left to read a response, so
		// only note the event at debug level.
		h.logger.DebugContext(r.Context(), "request canceled by client",
			"request_id", requestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
		)

	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request took too long to process")

	default:
		h.logger.ErrorContext(r.Context(), "request failed",
			"request_id", requestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

// intQuery parses an optional integer query parameter.
func intQuery(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("query parameter %q must be an integer", key)
	}
	return value, nil
}
