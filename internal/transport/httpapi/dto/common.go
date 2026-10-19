// Package dto contains Data Transfer Objects for the HTTP boundary.
//
// Go does not have a framework-level DTO concept like NestJS, but the pattern
// is idiomatic: define explicit structs for requests and responses, validate
// them at the edge, and map them to/from domain entities. Keeping DTOs separate
// from domain types means renaming a database column or adding a computed date
// field never changes the public contract by accident.
//
// Validation uses github.com/go-playground/validator/v10, the most widely used
// struct validator in Go. Tags such as `validate:"required,max=200"` are
// declarative and the same validator instance is reused across requests.
package dto

// ErrorResponse is the stable error shape used by every endpoint:
//
//	{"error":{"code":"not_found","message":"..."}}
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a machine-readable code and a human-readable message.
type ErrorDetail struct {
	Code    string `json:"code" example:"not_found"`
	Message string `json:"message" example:"the requested task does not exist"`
}

// HealthResponse is returned by /healthz and /readyz.
type HealthResponse struct {
	Status       string                      `json:"status" example:"ok"`
	Service      string                      `json:"service" example:"taskflow"`
	Time         string                      `json:"time" example:"2026-10-19T12:00:00Z"`
	Environment  string                      `json:"environment,omitempty" example:"development"`
	Dependencies map[string]DependencyHealth `json:"dependencies,omitempty"`
}

// DependencyHealth describes one dependency check.
type DependencyHealth struct {
	Status    string  `json:"status" example:"ok"`
	Error     string  `json:"error,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty" example:"1.42"`
}
