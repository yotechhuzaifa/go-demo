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

	"golang.org/x/sync/errgroup"
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
//
// Previous version, kept for comparison. The checks ran one after another in
// a plain loop:
//
//	for _, check := range c.checks {
//		pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
//		err := check.Ping(pingContext)
//		cancel()
//		...
//	}
//
// The comment said "concurrently" but the code was sequential, so the
// worst-case readiness latency was the sum of every timeout (three
// dependencies at three seconds each) instead of the slowest one.
//
// The version below uses golang.org/x/sync/errgroup. Every goroutine writes
// into its own slot of a pre-allocated slice, so no lock is needed for the
// results, and errgroup.Wait is the barrier that makes them visible.
func (c *Checker) CheckAll(ctx context.Context) (bool, map[string]Status) {
	results := make([]Status, len(c.checks))

	var group errgroup.Group
	for index, check := range c.checks {
		group.Go(func() error {
			pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			start := time.Now()
			err := check.Ping(pingContext)
			latency := float64(time.Since(start).Microseconds()) / 1000

			if err != nil {
				c.logger.ErrorContext(ctx, "health check failed",
					"dependency", check.Name,
					"latency_ms", latency,
					"error", err,
				)
				results[index] = Status{
					Status:    "error",
					Error:     fmt.Sprintf("%v", err),
					LatencyMS: latency,
				}
				return nil
			}

			c.logger.DebugContext(ctx, "health check succeeded",
				"dependency", check.Name, "latency_ms", latency)
			results[index] = Status{
				Status:    "ok",
				LatencyMS: latency,
			}
			return nil
		})
	}

	// Every goroutine records its own result and returns nil, so a failing
	// dependency does not cancel the others. The error is the per-dependency
	// failure that was already recorded in the result slot.
	_ = group.Wait()

	healthy := true
	byName := make(map[string]Status, len(c.checks))
	for index, check := range c.checks {
		if results[index].Status != "ok" {
			healthy = false
		}
		byName[check.Name] = results[index]
	}

	return healthy, byName
}
