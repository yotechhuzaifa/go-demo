package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxRequestBodyBytes limits how much data the server will read for one JSON
// request. This protects memory and is a cheap defense against abusive clients.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// envelope is the consistent success shape for single resources:
//
//	{"data": {...}}
//
// Generics keep the wrapper type-safe without repeating a struct for every
// payload. List responses use a dedicated struct because they also include meta.
type envelope[T any] struct {
	Data T `json:"data"`
}

// errorResponse is the consistent failure shape:
//
//	{"error": {"code": "not_found", "message": "..."}}
//
// A stable machine-readable code is easier for clients to branch on than
// parsing human-readable text.
type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON serializes a payload and writes it with the given status code.
//
// The payload is marshaled before any headers are written, so an encoding
// failure can still produce a clean 500 response. All of our response types are
// JSON-safe, but keeping this guard makes the helper safe to reuse.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":{"code":"internal_error","message":"failed to encode response"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n")) // Newline keeps curl output readable.
}

// writeError sends a structured error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// decodeJSON strictly decodes a single JSON object from the request body.
//
// Strictness is useful at a public API boundary:
//   - unknown fields are rejected, catching client typos early;
//   - trailing JSON values are rejected, preventing ambiguous requests;
//   - the body size is capped to avoid unbounded memory use.
func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}

	// A second decode must return EOF. Anything else means the client sent more
	// than one JSON value, which is not a valid request body.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}

	return nil
}
