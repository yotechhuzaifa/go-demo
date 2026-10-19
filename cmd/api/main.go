// Command api starts the taskflow HTTP server.
//
// This file is the composition root: the one place where concrete
// implementations are chosen and wired together. Keeping construction here
// means every other package can depend on interfaces or plain values instead of
// global state.
//
// Startup is deliberately verbose. Every step logs what it is doing, every
// connection logs its target (with the password redacted), every retry logs the
// underlying driver error, and shutdown logs each disconnect. When something
// fails, the first lines of the log should be enough to diagnose it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/taskflow/internal/config"
	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/health"
	"example.com/taskflow/internal/platform/logger"
	"example.com/taskflow/internal/storage/mongodb"
	"example.com/taskflow/internal/storage/postgres"
	rediscache "example.com/taskflow/internal/storage/redis"
	"example.com/taskflow/internal/transport/httpapi"
)

// version is replaced at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/api
//
// The default is useful for local `go run` builds.
var version = "dev"

// @title Taskflow API
// @version 1.0
// @description A verbose, production-shaped Go service demonstrating PostgreSQL, MongoDB, Redis, DTO validation, retry handling, and automated Swagger generation.
// @description
// @description Every task write goes to PostgreSQL, refreshes Redis, and appends an audit document to MongoDB. The overview endpoint reads all three stores in one request.
// @contact.name Taskflow maintainers
// @host localhost:8080
// @BasePath /
// @schemes http
func main() {
	// main should be a thin wrapper. Putting the real work in run() makes the
	// function testable and ensures deferred cleanup still executes.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "taskflow: %v\n", err)
		os.Exit(1)
	}
}

// run starts the server and blocks until a termination signal arrives or the
// listener fails. It returns an error for truly unexpected failures.
func run() error {
	// -----------------------------------------------------------------------
	// Step 1: configuration
	// -----------------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat, cfg.LogColor)
	log.Info("step 1/9: configuration loaded",
		"version", version,
		"env", cfg.Env,
		"addr", cfg.HTTPAddr,
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
		"log_color", cfg.LogColor,
		"postgres_schema", cfg.PostgresSchema,
		"mongo_database", cfg.MongoDatabase,
		"redis_addr", cfg.RedisAddr,
		"cache_ttl", cfg.CacheTTL,
	)

	// A startup context bounds the total time spent connecting. Cancellation is
	// also wired into the retry helpers, so Ctrl+C during a slow connection
	// stops promptly instead of waiting for every attempt.
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStartup()

	// -----------------------------------------------------------------------
	// Step 2: PostgreSQL
	// -----------------------------------------------------------------------
	log.Info("step 2/9: connecting to PostgreSQL")
	postgresDB, err := postgres.Connect(
		startupContext,
		cfg.PostgresURL,
		cfg.PostgresSchema,
		cfg.DBConnectAttempts,
		cfg.DBConnectRetryDelay,
		log,
	)
	if err != nil {
		return fmt.Errorf("postgres startup: %w", err)
	}
	defer postgresDB.Close()

	// -----------------------------------------------------------------------
	// Step 3: migrations
	// -----------------------------------------------------------------------
	if cfg.AutoMigrate {
		log.Info("step 3/9: applying PostgreSQL migrations")
		if err := postgres.Migrate(startupContext, cfg.PostgresURL, log); err != nil {
			return fmt.Errorf("postgres migration: %w", err)
		}
	} else {
		log.Warn("step 3/9: AUTO_MIGRATE=false; skipping PostgreSQL migrations")
	}

	// -----------------------------------------------------------------------
	// Step 4: MongoDB
	// -----------------------------------------------------------------------
	log.Info("step 4/9: connecting to MongoDB")
	mongoDB, err := mongodb.Connect(
		startupContext,
		cfg.MongoURI,
		cfg.MongoDatabase,
		cfg.MongoConnectTimeout,
		cfg.DBConnectAttempts,
		cfg.DBConnectRetryDelay,
		log,
	)
	if err != nil {
		return fmt.Errorf("mongodb startup: %w", err)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		mongoDB.Close(closeContext)
	}()

	if cfg.AutoMigrate {
		log.Info("step 4b/9: ensuring MongoDB collections and indexes")
		if err := mongoDB.EnsureSchema(startupContext); err != nil {
			return fmt.Errorf("mongodb schema: %w", err)
		}
	}

	// -----------------------------------------------------------------------
	// Step 5: Redis
	// -----------------------------------------------------------------------
	log.Info("step 5/9: connecting to Redis")
	cache, err := rediscache.Connect(startupContext, rediscache.Options{
		Addr:          cfg.RedisAddr,
		Username:      cfg.RedisUsername,
		Password:      cfg.RedisPassword,
		DB:            cfg.RedisDB,
		DialTimeout:   cfg.RedisDialTimeout,
		ReadTimeout:   cfg.RedisReadTimeout,
		WriteTimeout:  cfg.RedisWriteTimeout,
		RetryAttempts: cfg.RedisRetryAttempts,
		RetryDelay:    cfg.RedisRetryDelay,
		CacheTTL:      cfg.CacheTTL,
	}, log)
	if err != nil {
		return fmt.Errorf("redis startup: %w", err)
	}
	defer cache.Close()

	// -----------------------------------------------------------------------
	// Step 6: repositories and domain services
	// -----------------------------------------------------------------------
	log.Info("step 6/9: wiring repositories and domain services")

	taskRepository := postgres.NewTaskRepository(postgresDB, log)
	eventRepository := mongodb.NewEventRepository(mongoDB, log)
	noteRepository := mongodb.NewNoteRepository(mongoDB, log)

	taskService := task.NewService(
		taskRepository,
		task.WithCache(cache),
		task.WithEventRecorder(eventRepository),
		task.WithLogger(log),
	)
	noteService := note.NewService(noteRepository, note.WithLogger(log))

	// -----------------------------------------------------------------------
	// Step 7: demo data (idempotent)
	// -----------------------------------------------------------------------
	if cfg.SeedDemoData {
		log.Info("step 7/9: ensuring demo data exists")
		if err := seedDemoData(startupContext, taskService, noteService, log); err != nil {
			return fmt.Errorf("seed demo data: %w", err)
		}
	} else {
		log.Warn("step 7/9: SEED_DEMO_DATA=false; skipping demo data")
	}

	// -----------------------------------------------------------------------
	// Step 8: readiness checker and HTTP router
	// -----------------------------------------------------------------------
	log.Info("step 8/9: building HTTP router")
	checker := health.New(log,
		health.Check{Name: "postgresql", Ping: postgresDB.Ping},
		health.Check{Name: "mongodb", Ping: mongoDB.Ping},
		health.Check{Name: "redis", Ping: cache.Ping},
	)

	handler := httpapi.NewRouter(
		taskService,
		log,
		httpapi.WithNoteService(noteService),
		httpapi.WithCache(cache),
		httpapi.WithHealthChecker(checker),
		httpapi.WithEnvironment(cfg.Env),
		httpapi.WithSwagger(true),
	)

	// -----------------------------------------------------------------------
	// Step 9: HTTP server
	// -----------------------------------------------------------------------
	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,

		// Timeouts are essential in production. Without them, a slow or
		// malicious client can hold connections open indefinitely.
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,

		// Cap header size to reduce memory pressure from oversized requests.
		MaxHeaderBytes: 1 << 20, // 1 MiB

		// Route net/http's internal error messages through slog so every log
		// line has the same format and destination.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	// NotifyContext cancels when SIGINT (Ctrl+C) or SIGTERM (container stop)
	// arrives. The deferred stop releases the signal handler resources.
	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ListenAndServe blocks, so run it in a goroutine and report its result on a
	// buffered channel. Buffering prevents the goroutine from leaking if the
	// shutdown path returns early.
	serverErrors := make(chan error, 1)
	go func() {
		log.Info("step 9/9: HTTP server listening",
			"addr", cfg.HTTPAddr,
			"swagger", fmt.Sprintf("http://localhost%s/swagger/index.html", cfg.HTTPAddr),
		)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		// A clean ErrServerClosed here means Close or Shutdown was called
		// elsewhere. Anything else is a real startup/runtime failure.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)

	case <-shutdownSignal.Done():
		log.Info("shutdown: termination signal received")
	}

	// Stop accepting new connections and wait for in-flight requests. Use a
	// fresh context with a deadline because the signal context is already
	// canceled.
	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	log.Info("shutdown: draining HTTP connections", "timeout", cfg.ShutdownTimeout)
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Error("shutdown: HTTP drain failed", "error", err)
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	// Shutdown makes ListenAndServe return ErrServerClosed.
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	// Deferred infrastructure cleanup runs next, in reverse order:
	// Redis -> MongoDB -> PostgreSQL. Each Close logs its own result.
	log.Info("shutdown: HTTP server stopped; closing dependencies")
	log.Info("server stopped cleanly")
	return nil
}
