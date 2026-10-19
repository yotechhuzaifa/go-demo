// Package retryx centralizes retry behaviour for dependency calls.
//
// The project uses the well-known github.com/avast/retry-go/v4 package rather
// than re-implementing backoff loops. This wrapper adds two things the library
// does not know about:
//
//  1. a consistent, verbose log line for every failed attempt;
//  2. context cancellation, so a client disconnect stops the retries.
//
// The same helper is used for startup connections (PostgreSQL, MongoDB, Redis)
// and for Redis commands that may run while the connection is recovering.
package retryx

import (
	"context"
	"log/slog"
	"time"

	"github.com/avast/retry-go/v4"
)

// Do runs operation until it succeeds, the attempt budget is exhausted, or the
// context is canceled.
//
// operation is a short human-readable label such as "postgres.connect" or
// "redis.get". Every failed attempt is logged at warn level with the attempt
// number and the underlying error, which makes connection problems visible in
// the first log lines instead of only after the process exits.
//
// The final error is returned unchanged (wrapped by retry-go only when there is
// more than one attempt) so callers can inspect it with errors.Is/As.
func Do(
	ctx context.Context,
	logger *slog.Logger,
	operation string,
	attempts int,
	delay time.Duration,
	fn func() error,
) error {
	return DoIf(ctx, logger, operation, attempts, delay, func(error) bool { return true }, fn)
}

// DoIf is Do with a predicate that decides whether an error is retryable.
//
// It exists for cache calls: a Redis "key not found" response (redis.Nil) is a
// perfectly normal cache miss and must not be retried. Network errors still use
// the full attempt budget.
func DoIf(
	ctx context.Context,
	logger *slog.Logger,
	operation string,
	attempts int,
	delay time.Duration,
	shouldRetry func(error) bool,
	fn func() error,
) error {
	if attempts < 1 {
		attempts = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	if shouldRetry == nil {
		shouldRetry = func(error) bool { return true }
	}

	logger.Debug("retry: starting operation",
		"operation", operation,
		"attempts", attempts,
		"delay", delay,
	)

	err := retry.Do(
		fn,
		// retry.Context makes a canceled request stop immediately instead of
		// sleeping through the remaining attempts.
		retry.Context(ctx),
		retry.Attempts(uint(attempts)),
		retry.Delay(delay),
		retry.DelayType(retry.FixedDelay),
		retry.RetryIf(shouldRetry),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			// attempt is zero-based inside retry-go; add one for humans.
			logger.Warn("retry: attempt failed",
				"operation", operation,
				"attempt", attempt+1,
				"max_attempts", attempts,
				"error", err,
			)
		}),
	)

	if err != nil {
		// A non-retryable result (for example redis.Nil, a normal cache miss)
		// is expected control flow, not an operational failure. Logging it at
		// error level would make every cache miss look like an outage.
		if !shouldRetry(err) {
			logger.Debug("retry: operation returned a non-retryable result",
				"operation", operation,
				"error", err,
			)
			return err
		}

		logger.Error("retry: operation failed permanently",
			"operation", operation,
			"attempts", attempts,
			"error", err,
		)
		return err
	}

	logger.Debug("retry: operation succeeded", "operation", operation)
	return nil
}
