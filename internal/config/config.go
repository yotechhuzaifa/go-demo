// Package config loads runtime configuration from environment variables.
//
// Configuration follows the twelve-factor app pattern: the same binary runs in
// development, staging, and production by changing environment variables only.
// For local development the application also loads a `.env` file when present
// (using the well-known github.com/joho/godotenv package). A `.env` file is a
// convenience, never the production source of truth: real secrets should come
// from a secret manager in deployed environments.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds every runtime setting needed by the process.
//
// Values are intentionally plain types so they are easy to log, test, and pass
// to constructors. Secrets (URLs and passwords) are never logged directly; the
// connection packages redact them before emitting a log line.
type Config struct {
	// HTTP server ------------------------------------------------------------
	Env               string        // Deployment environment, e.g. "development".
	HTTPAddr          string        // Listen address, e.g. ":8080".
	ReadHeaderTimeout time.Duration // Protects against slow header attacks.
	ReadTimeout       time.Duration // Maximum time to read the whole request.
	WriteTimeout      time.Duration // Maximum time to write the response.
	IdleTimeout       time.Duration // How long keep-alive connections stay open.
	ShutdownTimeout   time.Duration // Grace period for in-flight requests.

	// Logging ----------------------------------------------------------------
	LogLevel  string // debug, info, warn, or error.
	LogFormat string // "text" for humans, "json" for machines.
	LogColor  string // "auto", "always", or "never". Colors only apply to text.

	// PostgreSQL -------------------------------------------------------------
	PostgresURL    string // Full connection string.
	PostgresSchema string // Dedicated schema owned by this project.

	// MongoDB ----------------------------------------------------------------
	MongoURI            string        // mongodb:// or mongodb+srv:// connection string.
	MongoDatabase       string        // Dedicated database owned by this project.
	MongoConnectTimeout time.Duration // Server selection timeout during startup.

	// Redis ------------------------------------------------------------------
	RedisAddr         string        // host:port.
	RedisUsername     string        // ACL username ("default" for most managed Redis).
	RedisPassword     string        // ACL password. Never logged.
	RedisDB           int           // Logical database number.
	RedisDialTimeout  time.Duration // TCP connect timeout.
	RedisReadTimeout  time.Duration // Socket read timeout.
	RedisWriteTimeout time.Duration // Socket write timeout.

	// Resilience -------------------------------------------------------------
	DBConnectAttempts   int           // Startup connection attempts per dependency.
	DBConnectRetryDelay time.Duration // Delay between startup attempts.
	RedisRetryAttempts  int           // Attempts per Redis command after a disconnect.
	RedisRetryDelay     time.Duration // Delay between Redis command attempts.
	CacheTTL            time.Duration // How long cached tasks stay in Redis.
	AutoMigrate         bool          // Run PostgreSQL + MongoDB setup on startup.
	SeedDemoData        bool          // Insert demo tasks/notes when tables are empty.
}

// Environment variable names live in one place so documentation and tests
// cannot drift from the implementation.
const (
	envAppEnv            = "APP_ENV"
	envHTTPAddr          = "HTTP_ADDR"
	envReadHeaderTimeout = "HTTP_READ_HEADER_TIMEOUT"
	envReadTimeout       = "HTTP_READ_TIMEOUT"
	envWriteTimeout      = "HTTP_WRITE_TIMEOUT"
	envIdleTimeout       = "HTTP_IDLE_TIMEOUT"
	envShutdownTimeout   = "SHUTDOWN_TIMEOUT"
	envLogLevel          = "LOG_LEVEL"
	envLogFormat         = "LOG_FORMAT"
	envLogColor          = "LOG_COLOR"

	envPostgresURL    = "POSTGRES_URL"
	envPostgresSchema = "POSTGRES_SCHEMA"

	envMongoURI            = "MONGO_URI"
	envMongoDatabase       = "MONGO_DATABASE"
	envMongoConnectTimeout = "MONGO_CONNECT_TIMEOUT"

	envRedisAddr         = "REDIS_ADDR"
	envRedisUsername     = "REDIS_USERNAME"
	envRedisPassword     = "REDIS_PASSWORD"
	envRedisDB           = "REDIS_DB"
	envRedisDialTimeout  = "REDIS_DIAL_TIMEOUT"
	envRedisReadTimeout  = "REDIS_READ_TIMEOUT"
	envRedisWriteTimeout = "REDIS_WRITE_TIMEOUT"

	envDBConnectAttempts   = "DB_CONNECT_ATTEMPTS"
	envDBConnectRetryDelay = "DB_CONNECT_RETRY_DELAY"
	envRedisRetryAttempts  = "REDIS_RETRY_ATTEMPTS"
	envRedisRetryDelay     = "REDIS_RETRY_DELAY"
	envCacheTTL            = "CACHE_TTL"
	envAutoMigrate         = "AUTO_MIGRATE"
	envSeedDemoData        = "SEED_DEMO_DATA"
)

// LoadDotEnv loads a local `.env` file when one exists.
//
// It never overwrites variables that are already set, so a container or shell
// export always wins over the file. Missing files are ignored on purpose: the
// binary must also work with plain environment variables.
func LoadDotEnv(paths ...string) {
	if len(paths) == 0 {
		// `.env` is the modern convention; `configs/local.env` is kept for
		// backwards compatibility with the original project layout.
		paths = []string{".env", "configs/local.env"}
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Load(path); err != nil {
			// A malformed file is worth surfacing, but it should not stop the
			// process when the variable is not needed. The caller can inspect
			// the returned configuration and the startup logs.
			fmt.Fprintf(os.Stderr, "config: could not load %s: %v\n", path, err)
		}
	}
}

// Load reads configuration from the environment and validates it.
//
// Missing or empty variables fall back to safe local-development defaults.
// Invalid values return an error so the process fails fast at startup rather
// than misbehaving later. Required dependency settings (database URLs and Redis
// address) are validated here too: a service that cannot reach its stores
// should never pretend to be healthy.
func Load() (Config, error) {
	// Load `.env` before reading anything else. Existing environment variables
	// are not overwritten by godotenv.Load.
	LoadDotEnv()

	cfg := Config{
		Env:               getEnv(envAppEnv, "development"),
		HTTPAddr:          getEnv(envHTTPAddr, ":8080"),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
		LogLevel:          getEnv(envLogLevel, "info"),
		LogFormat:         getEnv(envLogFormat, "text"),
		LogColor:          getEnv(envLogColor, "auto"),

		PostgresURL:    getEnv(envPostgresURL, ""),
		PostgresSchema: getEnv(envPostgresSchema, "taskflow"),

		MongoURI:            getEnv(envMongoURI, ""),
		MongoDatabase:       getEnv(envMongoDatabase, "taskflow"),
		MongoConnectTimeout: 10 * time.Second,

		RedisAddr:         getEnv(envRedisAddr, ""),
		RedisUsername:     getEnv(envRedisUsername, ""),
		RedisPassword:     getEnv(envRedisPassword, ""),
		RedisDialTimeout:  5 * time.Second,
		RedisReadTimeout:  3 * time.Second,
		RedisWriteTimeout: 3 * time.Second,

		DBConnectAttempts:   5,
		DBConnectRetryDelay: 2 * time.Second,
		RedisRetryAttempts:  4,
		RedisRetryDelay:     250 * time.Millisecond,
		CacheTTL:            5 * time.Minute,
		AutoMigrate:         true,
		SeedDemoData:        true,
	}

	var err error
	if cfg.ReadHeaderTimeout, err = durationEnv(envReadHeaderTimeout, cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadTimeout, err = durationEnv(envReadTimeout, cfg.ReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = durationEnv(envWriteTimeout, cfg.WriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = durationEnv(envIdleTimeout, cfg.IdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationEnv(envShutdownTimeout, cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}

	if cfg.MongoConnectTimeout, err = durationEnv(envMongoConnectTimeout, cfg.MongoConnectTimeout); err != nil {
		return Config{}, err
	}
	if cfg.RedisDialTimeout, err = durationEnv(envRedisDialTimeout, cfg.RedisDialTimeout); err != nil {
		return Config{}, err
	}
	if cfg.RedisReadTimeout, err = durationEnv(envRedisReadTimeout, cfg.RedisReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.RedisWriteTimeout, err = durationEnv(envRedisWriteTimeout, cfg.RedisWriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.DBConnectRetryDelay, err = durationEnv(envDBConnectRetryDelay, cfg.DBConnectRetryDelay); err != nil {
		return Config{}, err
	}
	if cfg.RedisRetryDelay, err = durationEnv(envRedisRetryDelay, cfg.RedisRetryDelay); err != nil {
		return Config{}, err
	}
	if cfg.CacheTTL, err = durationEnv(envCacheTTL, cfg.CacheTTL); err != nil {
		return Config{}, err
	}

	if cfg.RedisDB, err = intEnv(envRedisDB, cfg.RedisDB); err != nil {
		return Config{}, err
	}
	if cfg.DBConnectAttempts, err = intEnv(envDBConnectAttempts, cfg.DBConnectAttempts); err != nil {
		return Config{}, err
	}
	if cfg.RedisRetryAttempts, err = intEnv(envRedisRetryAttempts, cfg.RedisRetryAttempts); err != nil {
		return Config{}, err
	}
	if cfg.AutoMigrate, err = boolEnv(envAutoMigrate, cfg.AutoMigrate); err != nil {
		return Config{}, err
	}
	if cfg.SeedDemoData, err = boolEnv(envSeedDemoData, cfg.SeedDemoData); err != nil {
		return Config{}, err
	}

	// Normalize values that are effectively case-insensitive.
	cfg.LogLevel = strings.ToLower(cfg.LogLevel)
	cfg.LogFormat = strings.ToLower(cfg.LogFormat)
	cfg.LogColor = strings.ToLower(strings.TrimSpace(cfg.LogColor))
	cfg.PostgresSchema = strings.TrimSpace(cfg.PostgresSchema)
	cfg.MongoDatabase = strings.TrimSpace(cfg.MongoDatabase)

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks cross-field and range constraints.
func (c Config) validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("%s must not be empty", envHTTPAddr)
	}

	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{envReadHeaderTimeout, c.ReadHeaderTimeout},
		{envReadTimeout, c.ReadTimeout},
		{envWriteTimeout, c.WriteTimeout},
		{envIdleTimeout, c.IdleTimeout},
		{envShutdownTimeout, c.ShutdownTimeout},
		{envMongoConnectTimeout, c.MongoConnectTimeout},
		{envRedisDialTimeout, c.RedisDialTimeout},
		{envRedisReadTimeout, c.RedisReadTimeout},
		{envRedisWriteTimeout, c.RedisWriteTimeout},
		{envDBConnectRetryDelay, c.DBConnectRetryDelay},
		{envRedisRetryDelay, c.RedisRetryDelay},
		{envCacheTTL, c.CacheTTL},
	}
	for _, timeout := range timeouts {
		if timeout.value <= 0 {
			return fmt.Errorf("%s must be greater than zero", timeout.name)
		}
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
		// Supported by the logger package.
	default:
		return fmt.Errorf("%s must be one of debug, info, warn, or error", envLogLevel)
	}

	switch c.LogFormat {
	case "text", "json":
		// Both formats are supported by log/slog.
	default:
		return fmt.Errorf("%s must be either text or json", envLogFormat)
	}

	switch c.LogColor {
	case "auto", "always", "never":
		// Supported by the logger package. Colors only affect the text format.
	default:
		return fmt.Errorf("%s must be one of auto, always, or never", envLogColor)
	}

	// Dependency endpoints are required. Empty values usually mean the wrong
	// working directory (so `.env` was not found) or a missing deployment
	// secret. Failing here is clearer than a timeout later.
	if strings.TrimSpace(c.PostgresURL) == "" {
		return fmt.Errorf("%s must be set (copy .env.example to .env)", envPostgresURL)
	}
	if strings.TrimSpace(c.MongoURI) == "" {
		return fmt.Errorf("%s must be set (copy .env.example to .env)", envMongoURI)
	}
	if strings.TrimSpace(c.RedisAddr) == "" {
		return fmt.Errorf("%s must be set (copy .env.example to .env)", envRedisAddr)
	}

	// A PostgreSQL schema becomes part of SQL identifiers, so restrict it to
	// characters that cannot escape an identifier. This is a cheap, effective
	// guard against accidental injection through configuration.
	if !validIdentifier(c.PostgresSchema) {
		return fmt.Errorf("%s must be a simple SQL identifier (letters, digits, underscore)", envPostgresSchema)
	}
	// The bundled goose migration qualifies its objects with `taskflow`.
	// Supporting another schema means shipping a matching migration, so fail
	// fast instead of letting the first query discover the mismatch.
	if c.PostgresSchema != "taskflow" {
		return fmt.Errorf("%s=%q is not supported by the bundled migration; use \"taskflow\" or add a matching migration", envPostgresSchema, c.PostgresSchema)
	}
	if !validIdentifier(c.MongoDatabase) {
		return fmt.Errorf("%s must contain only letters, digits, or underscores", envMongoDatabase)
	}

	if c.RedisDB < 0 {
		return fmt.Errorf("%s must be zero or greater", envRedisDB)
	}
	if c.DBConnectAttempts < 1 {
		return fmt.Errorf("%s must be at least 1", envDBConnectAttempts)
	}
	if c.RedisRetryAttempts < 1 {
		return fmt.Errorf("%s must be at least 1", envRedisRetryAttempts)
	}

	return nil
}

// validIdentifier allows the conservative subset used by PostgreSQL and
// MongoDB database names in this project.
func validIdentifier(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char == '_':
		case char >= '0' && char <= '9':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// getEnv returns the environment value when it is set and non-empty.
// Treating an empty value as unset makes it easy to clear an inherited
// variable in a shell or a test without special-case logic.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

// durationEnv parses a Go duration such as "5s" or "1m30s".
func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration (example: 5s): %w", key, err)
	}
	return value, nil
}

// intEnv parses an integer environment variable.
func intEnv(key string, fallback int) (int, error) {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

// boolEnv parses common boolean spellings: true, false, 1, 0, yes, no.
func boolEnv(key string, fallback bool) (bool, error) {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", key, err)
	}
	return value, nil
}
