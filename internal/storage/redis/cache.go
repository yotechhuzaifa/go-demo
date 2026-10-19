// Package rediscache implements the task cache with go-redis.
//
// Resilience design:
//
//   - go-redis maintains a connection pool and reconnects automatically;
//   - every command is wrapped in retryx with a visible log line per attempt;
//   - cache misses (redis.Nil) are never retried, because they are normal;
//   - cache failures never fail the HTTP request: the task service treats Redis
//     as an optimization and falls back to PostgreSQL.
//
// The package name is `rediscache` to avoid colliding with the imported
// `github.com/redis/go-redis/v9` package name in callers.
package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/retryx"
)

// Options groups connection and retry settings.
type Options struct {
	Addr          string
	Username      string
	Password      string
	DB            int
	DialTimeout   time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
	CacheTTL      time.Duration
}

// Cache is the Redis-backed task cache.
type Cache struct {
	client        *redis.Client
	ttl           time.Duration
	retryAttempts int
	retryDelay    time.Duration
	logger        *slog.Logger
}

// Compile-time proof that the cache satisfies the domain port.
var _ task.TaskCache = (*Cache)(nil)

// Connect creates the client and verifies it with PING.
//
// A Redis ACL password is required by the managed instance used in this
// project. The password is passed to the driver but never logged.
func Connect(ctx context.Context, options Options, logger *slog.Logger) (*Cache, error) {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("redis: connecting",
		"addr", options.Addr,
		"db", options.DB,
		"username", options.Username,
		"attempts", options.RetryAttempts,
	)

	client := redis.NewClient(&redis.Options{
		Addr:         options.Addr,
		Username:     options.Username,
		Password:     options.Password,
		DB:           options.DB,
		DialTimeout:  options.DialTimeout,
		ReadTimeout:  options.ReadTimeout,
		WriteTimeout: options.WriteTimeout,
		// Retries are handled by retryx so every attempt is logged and cache
		// misses are excluded. -1 disables go-redis's hidden retry loop.
		MaxRetries:   -1,
		PoolSize:     10,
		MinIdleConns: 1,
	})

	cache := &Cache{
		client:        client,
		ttl:           options.CacheTTL,
		retryAttempts: options.RetryAttempts,
		retryDelay:    options.RetryDelay,
		logger:        logger,
	}

	err := retryx.Do(ctx, logger, "redis.connect", options.RetryAttempts, options.RetryDelay, func() error {
		pingContext, cancel := context.WithTimeout(ctx, options.DialTimeout)
		defer cancel()
		return client.Ping(pingContext).Err()
	})
	if err != nil {
		// Close the client even though the ping failed, so no background
		// goroutine or socket outlives the failed startup.
		if closeErr := client.Close(); closeErr != nil {
			logger.Error("redis: closing failed client also failed", "error", closeErr)
		}
		return nil, fmt.Errorf("connect to Redis: %w", err)
	}

	logger.Info("redis: connected",
		"addr", options.Addr,
		"db", options.DB,
		"cache_ttl", options.CacheTTL,
	)
	return cache, nil
}

// GetTask returns a cached task. A missing key is (zero, false, nil).
func (c *Cache) GetTask(ctx context.Context, id string) (task.Task, bool, error) {
	key := taskKey(id)
	start := time.Now()

	var payload string
	err := c.retryCommand(ctx, "redis.get_task", func() error {
		return c.client.Get(ctx, key).Scan(&payload)
	})
	if errors.Is(err, redis.Nil) {
		c.logger.DebugContext(ctx, "redis: cache miss", "key", key, "duration_ms", durationMS(start))
		return task.Task{}, false, nil
	}
	if err != nil {
		c.logger.ErrorContext(ctx, "redis: cache read failed", "key", key, "error", err)
		return task.Task{}, false, err
	}

	var value task.Task
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		// A corrupt cache entry must not break reads. Delete it and report a
		// miss so the service repopulates from PostgreSQL.
		c.logger.ErrorContext(ctx, "redis: cached JSON is corrupt; deleting key",
			"key", key, "error", err)
		if deleteErr := c.client.Del(ctx, key).Err(); deleteErr != nil {
			c.logger.ErrorContext(ctx, "redis: deleting corrupt key failed",
				"key", key, "error", deleteErr)
		}
		return task.Task{}, false, nil
	}

	c.logger.DebugContext(ctx, "redis: cache hit",
		"key", key, "task_id", id, "duration_ms", durationMS(start))
	return value, true, nil
}

// SetTask stores a task as JSON with the configured TTL.
func (c *Cache) SetTask(ctx context.Context, value task.Task) error {
	payload, err := json.Marshal(value)
	if err != nil {
		c.logger.ErrorContext(ctx, "redis: marshaling task failed",
			"task_id", value.ID, "error", err)
		return fmt.Errorf("marshal task for cache: %w", err)
	}

	key := taskKey(value.ID)
	start := time.Now()
	if err := c.retryCommand(ctx, "redis.set_task", func() error {
		return c.client.Set(ctx, key, payload, c.ttl).Err()
	}); err != nil {
		c.logger.ErrorContext(ctx, "redis: cache write failed",
			"key", key, "task_id", value.ID, "error", err)
		return err
	}

	c.logger.DebugContext(ctx, "redis: cache write succeeded",
		"key", key, "task_id", value.ID,
		"ttl", c.ttl, "duration_ms", durationMS(start))
	return nil
}

// DeleteTask removes a cached task after mutation or deletion.
func (c *Cache) DeleteTask(ctx context.Context, id string) error {
	key := taskKey(id)
	start := time.Now()
	if err := c.retryCommand(ctx, "redis.delete_task", func() error {
		return c.client.Del(ctx, key).Err()
	}); err != nil {
		c.logger.ErrorContext(ctx, "redis: cache delete failed", "key", key, "error", err)
		return err
	}
	c.logger.DebugContext(ctx, "redis: cache key deleted",
		"key", key, "duration_ms", durationMS(start))
	return nil
}

// IncrementTaskViews atomically increments the overview counter.
func (c *Cache) IncrementTaskViews(ctx context.Context, id string) (int64, error) {
	key := viewsKey(id)
	var count int64
	if err := c.retryCommand(ctx, "redis.increment_views", func() error {
		value, err := c.client.Incr(ctx, key).Result()
		if err != nil {
			return err
		}
		count = value
		return nil
	}); err != nil {
		c.logger.ErrorContext(ctx, "redis: counter increment failed", "key", key, "error", err)
		return 0, err
	}
	c.logger.DebugContext(ctx, "redis: counter incremented", "key", key, "count", count)
	return count, nil
}

// TaskViewCount returns the current overview counter (0 when missing).
func (c *Cache) TaskViewCount(ctx context.Context, id string) (int64, error) {
	key := viewsKey(id)
	var count int64
	err := c.retryCommand(ctx, "redis.get_views", func() error {
		return c.client.Get(ctx, key).Scan(&count)
	})
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		c.logger.ErrorContext(ctx, "redis: counter read failed", "key", key, "error", err)
		return 0, err
	}
	return count, nil
}

// Ping checks Redis connectivity for the readiness endpoint.
func (c *Cache) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis: client is not initialized")
	}
	return c.client.Ping(ctx).Err()
}

// Close releases the pool. Errors are logged because this is called during
// shutdown, when a failed cleanup should still be visible in operations.
func (c *Cache) Close() {
	if c == nil || c.client == nil {
		return
	}
	c.logger.Info("redis: closing client")
	if err := c.client.Close(); err != nil {
		c.logger.Error("redis: close failed", "error", err)
		return
	}
	c.logger.Info("redis: client closed")
}

// retryCommand retries a Redis command except for cache misses.
//
// redis.Nil is a normal "key not found" result, not a connectivity error.
// Retrying it would turn every cache miss into multiple round trips.
func (c *Cache) retryCommand(ctx context.Context, operation string, command func() error) error {
	return retryx.DoIf(
		ctx,
		c.logger,
		operation,
		c.retryAttempts,
		c.retryDelay,
		func(err error) bool { return !errors.Is(err, redis.Nil) },
		command,
	)
}

// taskKey is the single source of truth for cache key formatting.
func taskKey(id string) string {
	return "taskflow:task:" + id
}

// viewsKey tracks how often the cross-database overview was requested.
func viewsKey(id string) string {
	return "taskflow:task:" + id + ":views"
}

// durationMS converts a start time into fractional milliseconds for logs.
func durationMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
