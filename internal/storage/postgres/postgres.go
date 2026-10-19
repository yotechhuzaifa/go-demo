// Package postgres is the PostgreSQL adapter for the task domain.
//
// The adapter uses pgx/v5 directly (no ORM). In Go, explicit SQL through pgx or
// database/sql is the idiomatic default: queries are visible, index usage is
// predictable, and there is no reflection-heavy mapping layer. An ORM is a
// reasonable choice in some teams, but it is not the ecosystem's standard the
// way Prisma/TypeORM are in the Node.js world.
//
// Schema ownership:
//   - the project creates and uses the `taskflow` schema;
//   - no other schema or table is modified;
//   - goose records migration versions in the default `goose_db_version` table.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"example.com/taskflow/internal/platform/redact"
	"example.com/taskflow/internal/platform/retryx"
	"example.com/taskflow/migrations"
)

// DB wraps the connection pool and the settings shared by repositories.
type DB struct {
	Pool   *pgxpool.Pool
	Schema string
	Logger *slog.Logger
}

// Connect opens a pgx pool and verifies it with Ping.
//
// Connection setup is retried because managed databases and pooled endpoints
// (this project uses a Prisma pooler) may briefly refuse connections during
// startup or failover. Every failed attempt is logged by retryx with the
// underlying driver error.
func Connect(
	ctx context.Context,
	databaseURL string,
	schema string,
	attempts int,
	retryDelay time.Duration,
	logger *slog.Logger,
) (*DB, error) {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("postgres: connecting",
		"target", redact.URL(databaseURL),
		"schema", schema,
		"attempts", attempts,
	)

	var pool *pgxpool.Pool

	err := retryx.Do(ctx, logger, "postgres.connect", attempts, retryDelay, func() error {
		config, err := pgxpool.ParseConfig(databaseURL)
		if err != nil {
			return fmt.Errorf("parse connection string: %w", err)
		}

		// Pool limits are intentionally modest for a teaching service. Tune
		// MaxConns together with the database's own connection limit.
		config.MaxConns = 10
		config.MinConns = 1
		config.MaxConnLifetime = 30 * time.Minute
		config.MaxConnIdleTime = 5 * time.Minute
		config.HealthCheckPeriod = 30 * time.Second
		config.ConnConfig.ConnectTimeout = 10 * time.Second

		// The project's PostgreSQL endpoint is a transaction-mode pooler
		// (Prisma). Such poolers do not guarantee that a given named prepared
		// statement stays on the same backend connection, and pgx's extended
		// protocol uses prepared statements. Simple protocol avoids
		// "prepared statement name is already in use" errors at the cost of a
		// little parsing overhead per query.
		config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

		candidate, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			return fmt.Errorf("create pool: %w", err)
		}

		pingContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := candidate.Ping(pingContext); err != nil {
			// Close the half-open pool so failed attempts do not leak
			// connections while retries are running.
			candidate.Close()
			return fmt.Errorf("ping: %w", err)
		}

		pool = candidate
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	logger.Info("postgres: connected",
		"target", redact.URL(databaseURL),
		"max_connections", 10,
	)

	return &DB{Pool: pool, Schema: schema, Logger: logger}, nil
}

// Migrate applies embedded goose migrations.
//
// goose opens its own database/sql handle because it is migration-tool
// agnostic; the application repository keeps using pgx's native pool. Both
// point at the same database.
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("postgres: applying migrations", "target", redact.URL(databaseURL))
	start := time.Now()

	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse migration connection string: %w", err)
	}
	// Same pooler constraint as the application pool: use simple protocol so
	// goose's DDL statements do not depend on named prepared statements.
	connectionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*connectionConfig)
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Error("postgres: closing migration connection failed", "error", closeErr)
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration connection: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	goose.SetDialect("postgres")
	goose.SetLogger(gooseLogger{logger: logger})

	if err := goose.UpContext(ctx, db, "."); err != nil {
		logger.Error("postgres: migration failed", "error", err)
		return fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("postgres: migrations applied",
		"duration_ms", float64(time.Since(start).Microseconds())/1000)
	return nil
}

// Ping verifies that the pool can still reach PostgreSQL.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("postgres: pool is not initialized")
	}
	return db.Pool.Ping(ctx)
}

// Close releases every pooled connection.
//
// Close is called during graceful shutdown. It does not return an error in
// pgx, but failures are still surfaced in the log because "closed without
// error" is useful confirmation that shutdown completed.
func (db *DB) Close() {
	if db == nil || db.Pool == nil {
		return
	}
	db.Logger.Info("postgres: closing connection pool")
	db.Pool.Close()
	db.Logger.Info("postgres: connection pool closed")
}

// gooseLogger adapts goose's Printf-style logger to slog.
//
// goose's Fatalf normally exits the process. In a server we want the error to
// propagate to the caller so deferred cleanup (closing pools, flushing logs)
// still runs, so Fatalf logs at error level and does not exit.
type gooseLogger struct {
	logger *slog.Logger
}

// Printf implements goose.Logger.
func (l gooseLogger) Printf(format string, args ...any) {
	// goose ends its messages with a newline. The log handler adds its own, so
	// the trailing newline is removed to keep every log line on one row.
	l.logger.Info(strings.TrimRight(fmt.Sprintf(format, args...), "\r\n"))
}

// Fatalf implements goose.Logger without terminating the process.
func (l gooseLogger) Fatalf(format string, args ...any) {
	l.logger.Error(strings.TrimRight(fmt.Sprintf(format, args...), "\r\n"))
}
