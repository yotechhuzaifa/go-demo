package mongodb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"example.com/taskflow/internal/domain/note"
)

// NoteRepository implements note.Repository using the official MongoDB driver.
type NoteRepository struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

// Compile-time proof that the adapter satisfies the domain contract.
var _ note.Repository = (*NoteRepository)(nil)

// noteDocument is the BSON representation. Keeping it separate from note.Note
// means storage-specific tags never leak into the domain package.
type noteDocument struct {
	ID        string    `bson:"_id"`
	TaskID    string    `bson:"task_id,omitempty"`
	Author    string    `bson:"author"`
	Body      string    `bson:"body"`
	Tags      []string  `bson:"tags"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// NewNoteRepository builds the adapter.
func NewNoteRepository(db *DB, logger *slog.Logger) *NoteRepository {
	if db == nil || db.Database == nil {
		panic("mongodb.NewNoteRepository: db must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &NoteRepository{
		collection: db.Database.Collection(CollectionNotes),
		logger:     logger,
	}
}

// Create inserts a note.
func (r *NoteRepository) Create(ctx context.Context, value *note.Note) error {
	start := time.Now()
	document := toNoteDocument(*value)

	r.logger.DebugContext(ctx, "mongodb: inserting note document",
		"collection", CollectionNotes, "note_id", value.ID)

	_, err := r.collection.InsertOne(ctx, document)
	if err != nil {
		r.logger.ErrorContext(ctx, "mongodb: note INSERT failed",
			"collection", CollectionNotes, "note_id", value.ID,
			"duration_ms", durationMS(start), "error", err)
		if mongo.IsDuplicateKeyError(err) {
			return note.ErrAlreadyExists
		}
		return fmt.Errorf("insert note: %w", err)
	}

	r.logger.InfoContext(ctx, "mongodb: note INSERT committed",
		"collection", CollectionNotes, "note_id", value.ID,
		"duration_ms", durationMS(start))
	return nil
}

// GetByID returns one note or note.ErrNotFound.
func (r *NoteRepository) GetByID(ctx context.Context, id string) (note.Note, error) {
	start := time.Now()

	r.logger.DebugContext(ctx, "mongodb: finding note document",
		"collection", CollectionNotes, "note_id", id)

	var document noteDocument
	err := r.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&document)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			r.logger.DebugContext(ctx, "mongodb: note not found",
				"collection", CollectionNotes, "note_id", id)
			return note.Note{}, note.ErrNotFound
		}
		r.logger.ErrorContext(ctx, "mongodb: note FIND failed",
			"collection", CollectionNotes, "note_id", id,
			"duration_ms", durationMS(start), "error", err)
		return note.Note{}, fmt.Errorf("find note: %w", err)
	}

	r.logger.InfoContext(ctx, "mongodb: note FIND completed",
		"collection", CollectionNotes, "note_id", id,
		"duration_ms", durationMS(start))
	return fromNoteDocument(document), nil
}

// List returns notes newest first.
func (r *NoteRepository) List(ctx context.Context, filter note.Filter) ([]note.Note, error) {
	start := time.Now()

	query := bson.M{}
	if filter.TaskID != "" {
		query["task_id"] = filter.TaskID
	}

	findOptions := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64(filter.Offset))
	if filter.Limit > 0 {
		findOptions.SetLimit(int64(filter.Limit))
	}

	r.logger.DebugContext(ctx, "mongodb: listing note documents",
		"collection", CollectionNotes,
		"task_id", filter.TaskID,
		"limit", filter.Limit,
		"offset", filter.Offset)

	cursor, err := r.collection.Find(ctx, query, findOptions)
	if err != nil {
		r.logger.ErrorContext(ctx, "mongodb: note LIST failed",
			"collection", CollectionNotes,
			"duration_ms", durationMS(start), "error", err)
		return nil, fmt.Errorf("list notes: %w", err)
	}
	defer func() {
		if closeErr := cursor.Close(ctx); closeErr != nil {
			r.logger.ErrorContext(ctx, "mongodb: closing note cursor failed", "error", closeErr)
		}
	}()

	documents := make([]noteDocument, 0, filter.Limit)
	if err := cursor.All(ctx, &documents); err != nil {
		r.logger.ErrorContext(ctx, "mongodb: decoding note cursor failed",
			"collection", CollectionNotes,
			"duration_ms", durationMS(start), "error", err)
		return nil, fmt.Errorf("decode notes: %w", err)
	}

	results := make([]note.Note, 0, len(documents))
	for _, document := range documents {
		results = append(results, fromNoteDocument(document))
	}

	r.logger.InfoContext(ctx, "mongodb: note LIST completed",
		"collection", CollectionNotes, "count", len(results),
		"duration_ms", durationMS(start))
	return results, nil
}

// CountByTask counts notes that reference a task.
func (r *NoteRepository) CountByTask(ctx context.Context, taskID string) (int64, error) {
	start := time.Now()

	r.logger.DebugContext(ctx, "mongodb: counting notes for task",
		"collection", CollectionNotes, "task_id", taskID)

	count, err := r.collection.CountDocuments(ctx, bson.M{"task_id": taskID})
	if err != nil {
		r.logger.ErrorContext(ctx, "mongodb: note COUNT failed",
			"collection", CollectionNotes, "task_id", taskID,
			"duration_ms", durationMS(start), "error", err)
		return 0, fmt.Errorf("count notes: %w", err)
	}

	r.logger.InfoContext(ctx, "mongodb: note COUNT completed",
		"collection", CollectionNotes, "task_id", taskID,
		"count", count, "duration_ms", durationMS(start))
	return count, nil
}

// Update replaces the mutable note fields and returns the updated document.
func (r *NoteRepository) Update(ctx context.Context, value *note.Note) error {
	start := time.Now()

	r.logger.DebugContext(ctx, "mongodb: updating note document",
		"collection", CollectionNotes, "note_id", value.ID)

	update := bson.M{
		"$set": bson.M{
			"task_id":    value.TaskID,
			"author":     value.Author,
			"body":       value.Body,
			"tags":       value.Tags,
			"updated_at": value.UpdatedAt,
		},
	}

	var updated noteDocument
	err := r.collection.FindOneAndUpdate(
		ctx,
		bson.M{"_id": value.ID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			r.logger.WarnContext(ctx, "mongodb: note UPDATE matched no document",
				"collection", CollectionNotes, "note_id", value.ID)
			return note.ErrNotFound
		}
		r.logger.ErrorContext(ctx, "mongodb: note UPDATE failed",
			"collection", CollectionNotes, "note_id", value.ID,
			"duration_ms", durationMS(start), "error", err)
		return fmt.Errorf("update note: %w", err)
	}

	// Keep the caller's value consistent with what was stored (MongoDB may
	// normalize types, and returning the persisted document is the safest
	// contract for the service layer).
	*value = fromNoteDocument(updated)

	r.logger.InfoContext(ctx, "mongodb: note UPDATE committed",
		"collection", CollectionNotes, "note_id", value.ID,
		"duration_ms", durationMS(start))
	return nil
}

// Delete removes a note.
func (r *NoteRepository) Delete(ctx context.Context, id string) error {
	start := time.Now()

	r.logger.DebugContext(ctx, "mongodb: deleting note document",
		"collection", CollectionNotes, "note_id", id)

	result, err := r.collection.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		r.logger.ErrorContext(ctx, "mongodb: note DELETE failed",
			"collection", CollectionNotes, "note_id", id,
			"duration_ms", durationMS(start), "error", err)
		return fmt.Errorf("delete note: %w", err)
	}
	if result.DeletedCount == 0 {
		r.logger.WarnContext(ctx, "mongodb: note DELETE matched no document",
			"collection", CollectionNotes, "note_id", id)
		return note.ErrNotFound
	}

	r.logger.InfoContext(ctx, "mongodb: note DELETE committed",
		"collection", CollectionNotes, "note_id", id,
		"duration_ms", durationMS(start))
	return nil
}

// toNoteDocument maps the domain entity to BSON.
func toNoteDocument(value note.Note) noteDocument {
	return noteDocument{
		ID:        value.ID,
		TaskID:    value.TaskID,
		Author:    value.Author,
		Body:      value.Body,
		Tags:      value.Tags,
		CreatedAt: value.CreatedAt.UTC(),
		UpdatedAt: value.UpdatedAt.UTC(),
	}
}

// fromNoteDocument maps BSON back to the domain entity. Timestamps are forced
// to UTC so callers never have to reason about the server's location.
func fromNoteDocument(document noteDocument) note.Note {
	tags := document.Tags
	if tags == nil {
		tags = []string{}
	}
	return note.Note{
		ID:        document.ID,
		TaskID:    document.TaskID,
		Author:    document.Author,
		Body:      document.Body,
		Tags:      tags,
		CreatedAt: document.CreatedAt.UTC(),
		UpdatedAt: document.UpdatedAt.UTC(),
	}
}

// durationMS converts a start time into fractional milliseconds for logs.
func durationMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
