package retryx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), testLogger(), "test.retry", 3, time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary failure")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Do returned an unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestDoIfStopsRetryingWhenPredicateRejectsError(t *testing.T) {
	sentinel := errors.New("permanent")
	attempts := 0

	err := DoIf(
		context.Background(),
		testLogger(),
		"test.no_retry",
		5,
		time.Millisecond,
		func(error) bool { return false },
		func() error {
			attempts++
			return sentinel
		},
	)

	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want sentinel", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 because the predicate rejected the error", attempts)
	}
}

func TestDoStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Do(ctx, testLogger(), "test.canceled", 5, time.Second, func() error {
		return errors.New("should not be attempted")
	})
	if err == nil {
		t.Fatal("Do error = nil, want a cancellation error")
	}
}
