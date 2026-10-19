package mongodb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/mongo"

	"example.com/taskflow/internal/domain/task"
)

// EventRepository stores append-only task audit events in MongoDB.
type EventRepository struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

// Compile-time proof that the adapter satisfies the domain contract.
var _ task.EventRecorder = (*EventRepository)(nil)

// taskEventDocument is the BSON shape of an audit event.
//
// The snapshot is an embedded document. Storing the whole task means an event
// remains useful even after later updates or a delete.
type taskEventDocument struct {
	ID         string               `bson:"_id"`
	TaskID     string               `bson:"task_id"`
	Type       string               `bson:"type"`
	Snapshot   taskSnapshotDocument `bson:"snapshot"`
	OccurredAt time.Time            `bson:"occurred_at"`
}

// taskSnapshotDocument mirrors task.Task with BSON tags.
type taskSnapshotDocument struct {
	ID          string     `bson:"id"`
	Title       string     `bson:"title"`
	Description string     `bson:"description"`
	Status      string     `bson:"status"`
	Priority    string     `bson:"priority"`
	DueDate     *time.Time `bson:"due_date,omitempty"`
	CreatedAt   time.Time  `bson:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at"`
}

// NewEventRepository builds the adapter.
func NewEventRepository(db *DB, logger *slog.Logger) *EventRepository {
	if db == nil || db.Database == nil {
		panic("mongodb.NewEventRepository: db must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &EventRepository{
		collection: db.Database.Collection(CollectionTaskEvents),
		logger:     logger,
	}
}

// RecordTaskEvent writes one audit document.
func (r *EventRepository) RecordTaskEvent(ctx context.Context, event task.TaskEvent) error {
	start := time.Now()

	document := taskEventDocument{
		ID:         event.ID,
		TaskID:     event.TaskID,
		Type:       string(event.Type),
		Snapshot:   toTaskSnapshot(event.Snapshot),
		OccurredAt: event.OccurredAt.UTC(),
	}

	r.logger.DebugContext(ctx, "mongodb: inserting audit event",
		"collection", CollectionTaskEvents,
		"event_id", event.ID,
		"task_id", event.TaskID,
		"event_type", event.Type)

	if _, err := r.collection.InsertOne(ctx, document); err != nil {
		r.logger.ErrorContext(ctx, "mongodb: audit event INSERT failed",
			"collection", CollectionTaskEvents,
			"event_id", event.ID,
			"task_id", event.TaskID,
			"duration_ms", durationMS(start),
			"error", err)
		return fmt.Errorf("insert task event: %w", err)
	}

	r.logger.InfoContext(ctx, "mongodb: audit event committed",
		"collection", CollectionTaskEvents,
		"event_id", event.ID,
		"task_id", event.TaskID,
		"event_type", event.Type,
		"duration_ms", durationMS(start))
	return nil
}

// toTaskSnapshot maps the domain entity into the embedded BSON document.
func toTaskSnapshot(value task.Task) taskSnapshotDocument {
	return taskSnapshotDocument{
		ID:          value.ID,
		Title:       value.Title,
		Description: value.Description,
		Status:      string(value.Status),
		Priority:    string(value.Priority),
		DueDate:     value.DueDate,
		CreatedAt:   value.CreatedAt.UTC(),
		UpdatedAt:   value.UpdatedAt.UTC(),
	}
}
