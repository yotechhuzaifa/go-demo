// Package httpapi exposes the task and note domains over a JSON HTTP API.
//
// It is an adapter: it owns HTTP concerns such as routing, status codes, JSON
// encoding, DTO validation, and middleware, but delegates all business
// decisions to the domain services. Swapping HTTP for gRPC would not require
// changing the domain.
package httpapi

import (
	"log/slog"
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "example.com/taskflow/docs/swagger" // Registers the generated Swagger document.
	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/health"
)

// serviceInfo describes the API root and acts as lightweight discoverability.
type serviceInfo struct {
	Service       string   `json:"service"`
	APIVersion    string   `json:"api_version"`
	Documentation string   `json:"documentation"`
	Endpoints     []string `json:"endpoints"`
}

// routerOptions holds optional collaborators. Keeping them in a struct makes
// NewRouter backwards-compatible with tests that only need the task service.
type routerOptions struct {
	notes         *note.Service
	cache         task.TaskCache
	health        *health.Checker
	environment   string
	enableSwagger bool
}

// Option customizes the router.
type Option func(*routerOptions)

// WithNoteService enables the MongoDB note CRUD routes.
func WithNoteService(service *note.Service) Option {
	return func(options *routerOptions) {
		options.notes = service
	}
}

// WithCache enables the cross-database overview endpoint.
func WithCache(cache task.TaskCache) Option {
	return func(options *routerOptions) {
		options.cache = cache
	}
}

// WithHealthChecker enables dependency checks in /readyz.
func WithHealthChecker(checker *health.Checker) Option {
	return func(options *routerOptions) {
		options.health = checker
	}
}

// WithEnvironment adds the environment label to health responses.
func WithEnvironment(environment string) Option {
	return func(options *routerOptions) {
		options.environment = environment
	}
}

// WithSwagger serves the generated Swagger UI at /swagger/index.html.
func WithSwagger(enabled bool) Option {
	return func(options *routerOptions) {
		options.enableSwagger = enabled
	}
}

// NewRouter builds the complete HTTP handler, including middleware.
//
// Route patterns use Go 1.22+ method and wildcard syntax. For example,
// "PATCH /api/v1/tasks/{id}" only matches that method and exposes the path
// segment through r.PathValue("id").
//
// The signature keeps the original NewRouter(service, logger) call working;
// notes, cache, health checks, and Swagger are added through functional options
// by the composition root.
func NewRouter(service *task.Service, logger *slog.Logger, options ...Option) http.Handler {
	if service == nil {
		panic("httpapi.NewRouter: service must not be nil")
	}
	if logger == nil {
		// Defensive fallback; production wiring always passes an explicit
		// logger so tests can capture or discard output.
		logger = slog.Default()
	}

	config := routerOptions{environment: "development"}
	for _, option := range options {
		option(&config)
	}

	handler := newTaskHandler(service, config.notes, config.cache, logger)
	healthHandler := newHealthHandler(config.health, config.environment, logger)

	mux := http.NewServeMux()

	// Root discovery endpoint. The {$} marker means "exactly this path", not a
	// subtree, so unknown paths still produce the router's normal 404.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		endpoints := []string{
			"GET /healthz",
			"GET /readyz",
			"GET /swagger/index.html",
			"GET /api/v1/tasks",
			"POST /api/v1/tasks",
			"GET /api/v1/tasks/{id}",
			"PATCH /api/v1/tasks/{id}",
			"DELETE /api/v1/tasks/{id}",
			"GET /api/v1/tasks/{id}/overview",
		}
		if config.notes != nil {
			endpoints = append(endpoints,
				"GET /api/v1/notes",
				"POST /api/v1/notes",
				"GET /api/v1/notes/{id}",
				"PATCH /api/v1/notes/{id}",
				"DELETE /api/v1/notes/{id}",
			)
		}

		writeJSON(w, http.StatusOK, envelope[serviceInfo]{
			Data: serviceInfo{
				Service:       "taskflow",
				APIVersion:    "v1",
				Documentation: "/swagger/index.html",
				Endpoints:     endpoints,
			},
		})
	})

	// Liveness: is the process running?
	mux.HandleFunc("GET /healthz", healthHandler.live)

	// Readiness: can this instance serve traffic? Checks PostgreSQL, MongoDB,
	// and Redis and includes their concrete errors in the response.
	mux.HandleFunc("GET /readyz", healthHandler.ready)

	// Resource routes: PostgreSQL-backed tasks.
	mux.HandleFunc("GET /api/v1/tasks", handler.list)
	mux.HandleFunc("POST /api/v1/tasks", handler.create)
	mux.HandleFunc("GET /api/v1/tasks/{id}", handler.get)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", handler.update)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", handler.delete)

	// Cross-database aggregation: PostgreSQL + MongoDB + Redis.
	mux.HandleFunc("GET /api/v1/tasks/{id}/overview", handler.overview)

	// MongoDB-backed notes.
	if config.notes != nil {
		notes := newNoteHandler(config.notes, logger)
		mux.HandleFunc("GET /api/v1/notes", notes.list)
		mux.HandleFunc("POST /api/v1/notes", notes.create)
		mux.HandleFunc("GET /api/v1/notes/{id}", notes.get)
		mux.HandleFunc("PATCH /api/v1/notes/{id}", notes.update)
		mux.HandleFunc("DELETE /api/v1/notes/{id}", notes.delete)
	}

	// Swagger UI is generated from handler annotations by `make docs`.
	if config.enableSwagger {
		mux.Handle("/swagger/", httpSwagger.Handler(
			httpSwagger.URL("/swagger/doc.json"),
		))
	}

	// Middleware runs outermost first. Recovery sits inside logging so a panic
	// is converted to a 500 that the logger can still record.
	return chain(mux,
		withRequestID,
		withLogging(logger),
		withRecovery(logger),
	)
}
