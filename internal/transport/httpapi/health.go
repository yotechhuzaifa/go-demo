package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"example.com/taskflow/internal/platform/health"
	"example.com/taskflow/internal/transport/httpapi/dto"
)

// healthHandler serves liveness and readiness probes.
type healthHandler struct {
	checker *health.Checker
	env     string
	logger  *slog.Logger
}

// newHealthHandler builds the probes.
func newHealthHandler(checker *health.Checker, env string, logger *slog.Logger) *healthHandler {
	return &healthHandler{checker: checker, env: env, logger: logger}
}

// live handles GET /healthz.
//
// @Summary Liveness probe
// @Description Answers whether the process is running. It does not touch external dependencies.
// @Tags Health
// @Produce json
// @Success 200 {object} dto.HealthResponse
// @Router /healthz [get]
func (h *healthHandler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, dto.HealthResponse{
		Status:      "ok",
		Service:     "taskflow",
		Time:        time.Now().UTC().Format(time.RFC3339),
		Environment: h.env,
	})
}

// ready handles GET /readyz.
//
// @Summary Readiness probe
// @Description Pings PostgreSQL, MongoDB, and Redis. Returns 503 with the concrete error for every unhealthy dependency.
// @Tags Health
// @Produce json
// @Success 200 {object} dto.HealthResponse
// @Failure 503 {object} dto.HealthResponse
// @Router /readyz [get]
func (h *healthHandler) ready(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()

	// With no checker configured (unit tests), the process is trivially ready.
	if h.checker == nil {
		writeJSON(w, http.StatusOK, dto.HealthResponse{
			Status:      "ok",
			Service:     "taskflow",
			Time:        now.Format(time.RFC3339),
			Environment: h.env,
		})
		return
	}

	healthy, results := h.checker.CheckAll(r.Context())
	dependencies := make(map[string]dto.DependencyHealth, len(results))
	for name, status := range results {
		dependencies[name] = dto.DependencyHealth{
			Status:    status.Status,
			Error:     status.Error,
			LatencyMS: status.LatencyMS,
		}
	}

	statusCode := http.StatusOK
	statusLabel := "ok"
	if !healthy {
		statusCode = http.StatusServiceUnavailable
		statusLabel = "error"
		h.logger.ErrorContext(r.Context(), "readiness check failed",
			"request_id", requestIDFromContext(r.Context()),
			"dependencies", dependencies,
		)
	}

	writeJSON(w, statusCode, dto.HealthResponse{
		Status:       statusLabel,
		Service:      "taskflow",
		Time:         now.Format(time.RFC3339),
		Environment:  h.env,
		Dependencies: dependencies,
	})
}
