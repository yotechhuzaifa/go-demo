package task

import (
	"context"
	"time"
)

// TaskCache is the small slice of cache behaviour the domain needs.
//
// The interface is defined here (where it is consumed), not in the Redis
// adapter. That is the same dependency-inversion rule used by Repository: the
// domain describes what it needs, and infrastructure satisfies it. The cache is
// optional; a Service created without one simply talks to PostgreSQL every time.
//
// Every method must be safe to call concurrently. Implementations should treat
// "not found" as a normal miss, not an error.
type TaskCache interface {
	// GetTask returns a cached task and whether it was found.
	GetTask(ctx context.Context, id string) (Task, bool, error)

	// SetTask caches a task for the configured TTL.
	SetTask(ctx context.Context, task Task) error

	// DeleteTask removes a cached task after an update or delete.
	DeleteTask(ctx context.Context, id string) error

	// IncrementTaskViews counts how often the aggregated task view was read.
	// It is deliberately part of the cache port: Redis is excellent at atomic
	// counters, and the overview endpoint needs a cross-database read anyway.
	IncrementTaskViews(ctx context.Context, id string) (int64, error)

	// TaskViewCount returns the current counter value. Missing counters are 0.
	TaskViewCount(ctx context.Context, id string) (int64, error)
}

// TaskEventType is a stable label stored with every audit document in MongoDB.
type TaskEventType string

const (
	EventTaskCreated TaskEventType = "task.created"
	EventTaskUpdated TaskEventType = "task.updated"
	EventTaskDeleted TaskEventType = "task.deleted"
)

// TaskEvent is an audit record. MongoDB stores one document per event, which
// gives the project an append-only history without complicating the relational
// schema. The full snapshot makes events useful even after later updates.
type TaskEvent struct {
	ID         string        // UUID of the event itself.
	TaskID     string        // Task the event belongs to.
	Type       TaskEventType // created, updated, or deleted.
	Snapshot   Task          // State of the task at the time of the event.
	OccurredAt time.Time     // UTC timestamp.
}

// EventRecorder persists audit events. Implementations must be safe for
// concurrent use. A failure to record an event is logged but does not roll back
// the primary PostgreSQL write: the database remains the source of truth and
// audit trails must not make the API unavailable.
type EventRecorder interface {
	RecordTaskEvent(ctx context.Context, event TaskEvent) error
}
