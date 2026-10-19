package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/transport/httpapi/dto"
)

// noteHandler exposes the MongoDB-backed note CRUD API.
type noteHandler struct {
	service *note.Service
	logger  *slog.Logger
}

// newNoteHandler constructs the handler set.
func newNoteHandler(service *note.Service, logger *slog.Logger) *noteHandler {
	return &noteHandler{service: service, logger: logger}
}

// create handles POST /api/v1/notes.
//
// @Summary Create a note
// @Description Inserts a note document into MongoDB.
// @Tags Notes
// @Accept json
// @Produce json
// @Param request body dto.CreateNoteRequest true "Note to create"
// @Success 201 {object} dto.NoteResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/notes [post]
func (h *noteHandler) create(w http.ResponseWriter, r *http.Request) {
	var request dto.CreateNoteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateDTO(request); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	created, err := h.service.Create(r.Context(), note.CreateInput{
		TaskID: request.TaskID,
		Author: request.Author,
		Body:   request.Body,
		Tags:   request.Tags,
	})
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/notes/"+created.ID)
	h.logger.InfoContext(r.Context(), "handler.notes.create: created",
		"request_id", requestIDFromContext(r.Context()), "note_id", created.ID)

	writeJSON(w, http.StatusCreated, dto.NoteResponse{Data: noteToDTO(created)})
}

// list handles GET /api/v1/notes.
//
// @Summary List notes
// @Description Returns notes newest first. Optionally filter by task_id.
// @Tags Notes
// @Produce json
// @Param task_id query string false "Only notes attached to this task UUID"
// @Param limit query int false "Page size (default 20, max 100)"
// @Param offset query int false "Number of notes to skip"
// @Success 200 {object} dto.NoteListResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/notes [get]
func (h *noteHandler) list(w http.ResponseWriter, r *http.Request) {
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

	filter := note.Filter{
		TaskID: query.Get("task_id"),
		Limit:  limit,
		Offset: offset,
	}

	h.logger.DebugContext(r.Context(), "handler.notes.list: calling service",
		"request_id", requestIDFromContext(r.Context()),
		"task_id", filter.TaskID, "limit", filter.Limit, "offset", filter.Offset)

	notes, err := h.service.List(r.Context(), filter)
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	effectiveLimit := limit
	if effectiveLimit == 0 {
		effectiveLimit = note.DefaultListLimit
	}
	if effectiveLimit > note.MaxListLimit {
		effectiveLimit = note.MaxListLimit
	}

	h.logger.InfoContext(r.Context(), "handler.notes.list: responding",
		"request_id", requestIDFromContext(r.Context()), "count", len(notes))

	writeJSON(w, http.StatusOK, dto.NoteListResponse{
		Data: notesToDTO(notes),
		Meta: dto.NoteListMeta{
			Limit:  effectiveLimit,
			Offset: offset,
			Count:  len(notes),
		},
	})
}

// get handles GET /api/v1/notes/{id}.
//
// @Summary Get one note
// @Tags Notes
// @Produce json
// @Param id path string true "Note UUID"
// @Success 200 {object} dto.NoteResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/notes/{id} [get]
func (h *noteHandler) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.respondError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.NoteResponse{Data: noteToDTO(found)})
}

// update handles PATCH /api/v1/notes/{id}.
//
// @Summary Partially update a note
// @Tags Notes
// @Accept json
// @Produce json
// @Param id path string true "Note UUID"
// @Param request body dto.UpdateNoteRequest true "Fields to update"
// @Success 200 {object} dto.NoteResponse
// @Failure 400 {object} dto.ErrorResponse
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/notes/{id} [patch]
func (h *noteHandler) update(w http.ResponseWriter, r *http.Request) {
	var request dto.UpdateNoteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateDTO(request); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	updated, err := h.service.Update(r.Context(), r.PathValue("id"), note.UpdateInput{
		TaskID: request.TaskID,
		Author: request.Author,
		Body:   request.Body,
		Tags:   request.Tags,
	})
	if err != nil {
		h.respondError(w, r, err)
		return
	}

	h.logger.InfoContext(r.Context(), "handler.notes.update: updated",
		"request_id", requestIDFromContext(r.Context()), "note_id", updated.ID)
	writeJSON(w, http.StatusOK, dto.NoteResponse{Data: noteToDTO(updated)})
}

// delete handles DELETE /api/v1/notes/{id}.
//
// @Summary Delete a note
// @Tags Notes
// @Param id path string true "Note UUID"
// @Success 204 "Note deleted; no body is returned"
// @Failure 404 {object} dto.ErrorResponse
// @Failure 500 {object} dto.ErrorResponse
// @Router /api/v1/notes/{id} [delete]
func (h *noteHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.service.Delete(r.Context(), id); err != nil {
		h.respondError(w, r, err)
		return
	}
	h.logger.InfoContext(r.Context(), "handler.notes.delete: deleted",
		"request_id", requestIDFromContext(r.Context()), "note_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// respondError maps note domain errors to HTTP responses.
func (h *noteHandler) respondError(w http.ResponseWriter, r *http.Request, err error) {
	var validationError *note.ValidationError

	switch {
	case errors.As(err, &validationError):
		writeError(w, http.StatusBadRequest, "validation_error", validationError.Error())

	case errors.Is(err, note.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "the requested note does not exist")

	case errors.Is(err, note.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "conflict", "a note with that identifier already exists")

	case errors.Is(err, context.Canceled):
		h.logger.DebugContext(r.Context(), "note request canceled by client",
			"request_id", requestIDFromContext(r.Context()))

	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request took too long to process")

	default:
		h.logger.ErrorContext(r.Context(), "note request failed",
			"request_id", requestIDFromContext(r.Context()),
			"method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}
