package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/storage/memory"
	"example.com/taskflow/internal/transport/httpapi"
)

// newTestHandler wires the real service and in-memory repository together.
//
// These are integration-style tests: they exercise routing, middleware, JSON
// handling, the service, and storage as one unit. Unit tests for individual
// layers live next to those packages.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	// Discard log output so passing tests stay quiet.
	testLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewRouter(
		task.NewService(memory.NewTaskRepository()),
		testLogger,
	)
}

// doRequest sends a JSON request through the handler and returns the recorder.
func doRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// doRawRequest is useful for malformed JSON that cannot be produced by
// json.Marshal.
func doRawRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// decodeBody is a small generic helper that removes repeated JSON boilerplate.
func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response body %q: %v", recorder.Body.String(), err)
	}
	return value
}

// taskEnvelope mirrors the single-resource response shape.
type taskEnvelope struct {
	Data task.Task `json:"data"`
}

// apiError mirrors the error response shape.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestTaskCRUDFlow(t *testing.T) {
	handler := newTestHandler(t)

	// Create ---------------------------------------------------------------
	createRecorder := doRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]string{
		"title":       "Write integration tests",
		"description": "Cover the happy path",
		"priority":    "high",
	})
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d; body = %s",
			createRecorder.Code, http.StatusCreated, createRecorder.Body.String())
	}

	created := decodeBody[taskEnvelope](t, createRecorder)
	if created.Data.ID == "" {
		t.Fatal("created task ID is empty")
	}
	if created.Data.Title != "Write integration tests" {
		t.Errorf("created Title = %q, want %q", created.Data.Title, "Write integration tests")
	}
	if created.Data.Status != task.StatusTodo {
		t.Errorf("created Status = %q, want %q", created.Data.Status, task.StatusTodo)
	}
	if created.Data.Priority != task.PriorityHigh {
		t.Errorf("created Priority = %q, want %q", created.Data.Priority, task.PriorityHigh)
	}

	wantLocation := "/api/v1/tasks/" + created.Data.ID
	if location := createRecorder.Header().Get("Location"); location != wantLocation {
		t.Errorf("Location = %q, want %q", location, wantLocation)
	}
	if requestID := createRecorder.Header().Get("X-Request-ID"); requestID == "" {
		t.Error("X-Request-ID header is missing")
	}

	// List -----------------------------------------------------------------
	listRecorder := doRequest(t, handler, http.MethodGet, "/api/v1/tasks?status=todo", nil)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want %d", listRecorder.Code, http.StatusOK)
	}

	listed := decodeBody[struct {
		Data []task.Task `json:"data"`
		Meta struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
			Count  int `json:"count"`
		} `json:"meta"`
	}](t, listRecorder)

	if listed.Meta.Count != 1 || len(listed.Data) != 1 {
		t.Fatalf("list count = %d, data length = %d, want 1", listed.Meta.Count, len(listed.Data))
	}
	if listed.Meta.Limit != task.DefaultListLimit {
		t.Errorf("list limit = %d, want default %d", listed.Meta.Limit, task.DefaultListLimit)
	}

	// Get ------------------------------------------------------------------
	getRecorder := doRequest(t, handler, http.MethodGet, wantLocation, nil)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET one status = %d, want %d", getRecorder.Code, http.StatusOK)
	}
	fetched := decodeBody[taskEnvelope](t, getRecorder)
	if fetched.Data.ID != created.Data.ID {
		t.Errorf("fetched ID = %q, want %q", fetched.Data.ID, created.Data.ID)
	}

	// Patch ----------------------------------------------------------------
	patchRecorder := doRequest(t, handler, http.MethodPatch, wantLocation, map[string]string{
		"status": "done",
	})
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d; body = %s",
			patchRecorder.Code, http.StatusOK, patchRecorder.Body.String())
	}
	patched := decodeBody[taskEnvelope](t, patchRecorder)
	if patched.Data.Status != task.StatusDone {
		t.Errorf("patched Status = %q, want %q", patched.Data.Status, task.StatusDone)
	}
	// A partial patch must preserve the original title.
	if patched.Data.Title != created.Data.Title {
		t.Errorf("patched Title = %q, want unchanged %q", patched.Data.Title, created.Data.Title)
	}

	// Delete ---------------------------------------------------------------
	deleteRecorder := doRequest(t, handler, http.MethodDelete, wantLocation, nil)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want %d", deleteRecorder.Code, http.StatusNoContent)
	}
	if deleteRecorder.Body.Len() != 0 {
		t.Errorf("DELETE body = %q, want empty", deleteRecorder.Body.String())
	}

	// Verify the resource is gone and the error shape is consistent.
	missingRecorder := doRequest(t, handler, http.MethodGet, wantLocation, nil)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("GET deleted status = %d, want %d", missingRecorder.Code, http.StatusNotFound)
	}
	missing := decodeBody[apiError](t, missingRecorder)
	if missing.Error.Code != "not_found" {
		t.Errorf("error code = %q, want %q", missing.Error.Code, "not_found")
	}
}

func TestTaskValidationErrors(t *testing.T) {
	handler := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "empty title",
			method:     http.MethodPost,
			path:       "/api/v1/tasks",
			body:       map[string]string{"title": "   "},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "unsupported priority",
			method:     http.MethodPost,
			path:       "/api/v1/tasks",
			body:       map[string]string{"title": "Valid", "priority": "urgent"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "unknown JSON field",
			method:     http.MethodPost,
			path:       "/api/v1/tasks",
			body:       map[string]string{"title": "Valid", "owner": "someone"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "invalid query parameter",
			method:     http.MethodGet,
			path:       "/api/v1/tasks?limit=abc",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_query",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := doRequest(t, handler, test.method, test.path, test.body)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s",
					recorder.Code, test.wantStatus, recorder.Body.String())
			}

			response := decodeBody[apiError](t, recorder)
			if response.Error.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", response.Error.Code, test.wantCode)
			}
			if response.Error.Message == "" {
				t.Error("error message is empty")
			}
		})
	}
}

func TestMalformedJSON(t *testing.T) {
	handler := newTestHandler(t)

	recorder := doRawRequest(t, handler, http.MethodPost, "/api/v1/tasks", `{"title":`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	response := decodeBody[apiError](t, recorder)
	if response.Error.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", response.Error.Code, "invalid_json")
	}
}

func TestHealthAndRoot(t *testing.T) {
	handler := newTestHandler(t)

	healthRecorder := doRequest(t, handler, http.MethodGet, "/healthz", nil)
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", healthRecorder.Code, http.StatusOK)
	}
	if requestID := healthRecorder.Header().Get("X-Request-ID"); requestID == "" {
		t.Error("health response is missing X-Request-ID")
	}

	rootRecorder := doRequest(t, handler, http.MethodGet, "/", nil)
	if rootRecorder.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rootRecorder.Code, http.StatusOK)
	}

	root := decodeBody[struct {
		Data struct {
			Service    string   `json:"service"`
			APIVersion string   `json:"api_version"`
			Endpoints  []string `json:"endpoints"`
		} `json:"data"`
	}](t, rootRecorder)
	if root.Data.Service != "taskflow" {
		t.Errorf("root service = %q, want %q", root.Data.Service, "taskflow")
	}
	if len(root.Data.Endpoints) == 0 {
		t.Error("root endpoint list is empty")
	}
}

// TestMiddlewareAcceptsValidRequestID shows that tracing IDs can be propagated
// from an upstream service while invalid values are replaced.
func TestMiddlewareAcceptsValidRequestID(t *testing.T) {
	handler := newTestHandler(t)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", "trace-abc_123")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Request-ID"); got != "trace-abc_123" {
		t.Errorf("X-Request-ID = %q, want propagated value", got)
	}
}

// taskWithDates mirrors the date-related fields added to the task DTO.
type taskWithDates struct {
	Data struct {
		ID           string `json:"id"`
		DueDate      string `json:"due_date"`
		DueDateHuman string `json:"due_date_human"`
		DueWeek      string `json:"due_week"`
		IsOverdue    bool   `json:"is_overdue"`
		DaysUntilDue int    `json:"days_until_due"`
	} `json:"data"`
}

func TestCreateTaskParsesFlexibleDateInRequestTimezone(t *testing.T) {
	handler := newTestHandler(t)

	recorder := doRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]string{
		"title":    "Plan the release",
		"due_date": "2026-12-31",
		"timezone": "Asia/Karachi",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d; body = %s",
			recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	response := decodeBody[taskWithDates](t, recorder)
	if response.Data.DueDate != "2026-12-30T19:00:00Z" {
		t.Errorf("due_date = %q, want 2026-12-30T19:00:00Z", response.Data.DueDate)
	}
	if response.Data.DueWeek != "2026-W53" {
		t.Errorf("due_week = %q, want 2026-W53", response.Data.DueWeek)
	}
	if response.Data.DueDateHuman == "" {
		t.Error("due_date_human is empty, want a relative description")
	}
	if response.Data.DaysUntilDue <= 0 {
		t.Errorf("days_until_due = %d, want a positive number", response.Data.DaysUntilDue)
	}
	if response.Data.IsOverdue {
		t.Error("is_overdue = true, want false for a future deadline")
	}
}

func TestCreateTaskRejectsInvalidDate(t *testing.T) {
	handler := newTestHandler(t)

	recorder := doRequest(t, handler, http.MethodPost, "/api/v1/tasks", map[string]string{
		"title":    "Bad date",
		"due_date": "not-a-real-date",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want %d; body = %s",
			recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}

	response := decodeBody[apiError](t, recorder)
	if response.Error.Code != "validation_error" {
		t.Errorf("error code = %q, want validation_error", response.Error.Code)
	}
}
