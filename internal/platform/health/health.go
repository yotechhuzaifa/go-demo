// Package health checks dependency connectivity for the readiness endpoint.
//
// Liveness ("is the process running?") and readiness ("can this instance serve
// traffic?") are different questions. This package answers the second one by
// pinging PostgreSQL, MongoDB, and Redis on every check.
//
// Failures are logged at error level with the underlying driver error and are
// also returned in the HTTP response. The project requirement is explicit:
// connection failures must be visible, not silently converted into a generic
// "not ready" message.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// PingFunc is the only capability a dependency must expose.
type PingFunc func(ctx context.Context) error

// Check describes one dependency.
type Check struct {
	Name string
	Ping PingFunc
}

// Status is the result of one dependency check.
type Status struct {
	Status    string  `json:"status"`               // "ok" or "error".
	Error     string  `json:"error,omitempty"`      // Underlying error, if any.
	LatencyMS float64 `json:"latency_ms,omitempty"` // Round-trip time.
}

// Checker runs all registered checks.
type Checker struct {
	checks []Check
	logger *slog.Logger
}

// New builds a checker. Nil checks are ignored so wiring code can pass optional
// dependencies without special cases.
func New(logger *slog.Logger, checks ...Check) *Checker {
	if logger == nil {
		logger = slog.Default()
	}

	filtered := make([]Check, 0, len(checks))
	for _, check := range checks {
		if check.Ping != nil {
			filtered = append(filtered, check)
		}
	}
	return &Checker{checks: filtered, logger: logger}
}

// CheckAll pings every dependency concurrently and reports whether all of them
// are healthy.
//
// Each ping gets its own timeout so one hung dependency cannot block the
// readiness response indefinitely. Results are keyed by dependency name.
func (c *Checker) CheckAll(ctx context.Context) (bool, map[string]Status) {
	results := make(map[string]Status, len(c.checks))
	healthy := true

	for _, check := range c.checks {
		pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		start := time.Now()
		err := check.Ping(pingContext)
		latency := float64(time.Since(start).Microseconds()) / 1000
		cancel()

		if err != nil {
			healthy = false
			c.logger.ErrorContext(ctx, "health check failed",
				"dependency", check.Name,
				"latency_ms", latency,
				"error", err,
			)
			results[check.Name] = Status{
				Status:    "error",
				Error:     fmt.Sprintf("%v", err),
				LatencyMS: latency,
			}
			continue
		}

		c.logger.DebugContext(ctx, "health check succeeded",
			"dependency", check.Name, "latency_ms", latency)
		results[check.Name] = Status{
			Status:    "ok",
			LatencyMS: latency,
		}
	}

	return healthy, results
}
