// Package mongodb is the MongoDB adapter for notes and task audit events.
//
// Two collections are owned by this project inside the configured database
// (default: `taskflow`):
//
//	notes        full CRUD documents, optionally linked to a PostgreSQL task
//	task_events  append-only audit documents written after task mutations
//
// The adapter uses the official MongoDB Go driver. Indexes are created at
// startup because they are part of the application's data contract, not an
// optional performance tweak.
package mongodb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"example.com/taskflow/internal/platform/redact"
	"example.com/taskflow/internal/platform/retryx"
)

// Collection names are constants so the repositories and the index setup
// cannot disagree.
const (
	CollectionNotes      = "notes"
	CollectionTaskEvents = "task_events"
)

// DB wraps the MongoDB client and the database selected for this project.
type DB struct {
	Client   *mongo.Client
	Database *mongo.Database
	Logger   *slog.Logger
}

// Connect dials MongoDB and verifies the connection with Ping.
//
// The official driver already has retryable reads/writes. The explicit retry
// loop here covers the startup case where the server is not yet reachable at
// all (for example a paused Atlas cluster or a cold network path).
func Connect(
	ctx context.Context,
	uri string,
	database string,
	connectTimeout time.Duration,
	attempts int,
	retryDelay time.Duration,
	logger *slog.Logger,
) (*DB, error) {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("mongodb: connecting",
		"target", redact.URL(uri),
		"database", database,
		"attempts", attempts,
	)

	var client *mongo.Client

	err := retryx.Do(ctx, logger, "mongodb.connect", attempts, retryDelay, func() error {
		clientOptions := options.Client().
			ApplyURI(uri).
			SetConnectTimeout(connectTimeout).
			SetServerSelectionTimeout(connectTimeout).
			// Retryable writes make transient primary elections safer for
			// inserts and updates. Reads are retried as well.
			SetRetryWrites(true).
			SetRetryReads(true).
			SetMaxPoolSize(20)

		candidate, err := mongo.Connect(ctx, clientOptions)
		if err != nil {
			return fmt.Errorf("open client: %w", err)
		}

		pingContext, cancel := context.WithTimeout(ctx, connectTimeout)
		defer cancel()
		if err := candidate.Ping(pingContext, readpref.Primary()); err != nil {
			// Disconnect the failed client so retries do not leak goroutines
			// and sockets.
			_ = candidate.Disconnect(context.Background())
			return fmt.Errorf("ping: %w", err)
		}

		client = candidate
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}

	logger.Info("mongodb: connected",
		"target", redact.URL(uri), "database", database)

	return &DB{
		Client:   client,
		Database: client.Database(database),
		Logger:   logger,
	}, nil
}

// EnsureSchema creates collections and indexes.
//
// MongoDB creates a database and collection lazily on first write. Creating
// them explicitly makes the first request faster and lets index creation run at
// startup where failures are easy to see.
func (db *DB) EnsureSchema(ctx context.Context) error {
	if db == nil || db.Database == nil {
		return fmt.Errorf("mongodb: database is not initialized")
	}

	start := time.Now()
	db.Logger.Info("mongodb: ensuring collections and indexes",
		"database", db.Database.Name())

	for _, name := range []string{CollectionNotes, CollectionTaskEvents} {
		err := db.Database.CreateCollection(ctx, name)
		if err != nil {
			// Code 48 = NamespaceExists. It is expected on every restart.
			if commandError, ok := err.(mongo.CommandError); ok && commandError.Code == 48 {
				db.Logger.Debug("mongodb: collection already exists", "collection", name)
				continue
			}
			db.Logger.Error("mongodb: create collection failed",
				"collection", name, "error", err)
			return fmt.Errorf("create collection %s: %w", name, err)
		}
		db.Logger.Info("mongodb: collection created", "collection", name)
	}

	// Notes are commonly listed by task and newest first.
	noteIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "task_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_notes_task_created"),
		},
		{
			Keys:    bson.D{{Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_notes_created"),
		},
	}
	if _, err := db.Database.Collection(CollectionNotes).Indexes().CreateMany(ctx, noteIndexes); err != nil {
		db.Logger.Error("mongodb: create note indexes failed", "error", err)
		return fmt.Errorf("create note indexes: %w", err)
	}
	db.Logger.Info("mongodb: note indexes ready")

	// Audit events are queried per task, usually in chronological order.
	eventIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "task_id", Value: 1}, {Key: "occurred_at", Value: -1}},
			Options: options.Index().SetName("idx_task_events_task_occurred"),
		},
	}
	if _, err := db.Database.Collection(CollectionTaskEvents).Indexes().CreateMany(ctx, eventIndexes); err != nil {
		db.Logger.Error("mongodb: create event indexes failed", "error", err)
		return fmt.Errorf("create event indexes: %w", err)
	}
	db.Logger.Info("mongodb: event indexes ready")

	db.Logger.Info("mongodb: schema ready",
		"database", db.Database.Name(),
		"duration_ms", float64(time.Since(start).Microseconds())/1000)
	return nil
}

// Ping verifies that the client can still reach a primary.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Client == nil {
		return fmt.Errorf("mongodb: client is not initialized")
	}
	return db.Client.Ping(ctx, readpref.Primary())
}

// Close disconnects the client and reports failures.
func (db *DB) Close(ctx context.Context) {
	if db == nil || db.Client == nil {
		return
	}
	db.Logger.Info("mongodb: disconnecting")
	if err := db.Client.Disconnect(ctx); err != nil {
		db.Logger.Error("mongodb: disconnect failed", "error", err)
		return
	}
	db.Logger.Info("mongodb: disconnected")
}
