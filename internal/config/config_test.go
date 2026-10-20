package config

import (
	"testing"
	"time"
)

// environmentKeys lists every variable Load reads. Clearing them in tests keeps
// results independent of the developer's shell or CI environment.
var environmentKeys = []string{
	envAppEnv,
	envHTTPAddr,
	envReadHeaderTimeout,
	envReadTimeout,
	envWriteTimeout,
	envIdleTimeout,
	envShutdownTimeout,
	envLogLevel,
	envLogFormat,
	envLogColor,
	envPostgresURL,
	envMongoURI,
	envMongoDatabase,
	envMongoConnectTimeout,
	envRedisAddr,
	envRedisUsername,
	envRedisPassword,
	envRedisDB,
	envRedisDialTimeout,
	envRedisReadTimeout,
	envRedisWriteTimeout,
	envDBConnectAttempts,
	envDBConnectRetryDelay,
	envRedisRetryAttempts,
	envRedisRetryDelay,
	envCacheTTL,
	envAutoMigrate,
	envSeedDemoData,
}

// clearEnvironment sets each known variable to the empty string, which Load
// treats as "unset", then supplies the dependency endpoints that Load requires.
//
// Dependency URLs are mandatory in production because a service without its
// stores cannot do anything useful. Tests use harmless local placeholders; no
// connection is attempted by config.Load.
func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range environmentKeys {
		t.Setenv(key, "")
	}
	t.Setenv(envPostgresURL, "postgres://test:test@localhost:5432/taskflow")
	t.Setenv(envMongoURI, "mongodb://localhost:27017")
	t.Setenv(envRedisAddr, "localhost:6379")
}

func TestLoadDefaults(t *testing.T) {
	clearEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}

	// Spot-check the values a new developer gets with no configuration.
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want %q", cfg.Env, "development")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want %v", cfg.ReadTimeout, 10*time.Second)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, "text")
	}
	if cfg.LogColor != "auto" {
		t.Errorf("LogColor = %q, want %q", cfg.LogColor, "auto")
	}
	if cfg.MongoDatabase != "taskflow" {
		t.Errorf("MongoDatabase = %q, want %q", cfg.MongoDatabase, "taskflow")
	}
	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, 5*time.Minute)
	}
	if cfg.DBConnectAttempts != 5 {
		t.Errorf("DBConnectAttempts = %d, want 5", cfg.DBConnectAttempts)
	}
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate = false, want true by default")
	}
}

func TestLoadOverridesFromEnvironment(t *testing.T) {
	clearEnvironment(t)

	t.Setenv(envAppEnv, "production")
	t.Setenv(envHTTPAddr, "127.0.0.1:9090")
	t.Setenv(envReadHeaderTimeout, "2s")
	t.Setenv(envReadTimeout, "3s")
	t.Setenv(envWriteTimeout, "4s")
	t.Setenv(envIdleTimeout, "5s")
	t.Setenv(envShutdownTimeout, "6s")
	t.Setenv(envLogLevel, "DEBUG") // Should be normalized to lowercase.
	t.Setenv(envLogFormat, "JSON")
	t.Setenv(envLogColor, "ALWAYS")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}

	if cfg.Env != "production" {
		t.Errorf("Env = %q, want %q", cfg.Env, "production")
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, "127.0.0.1:9090")
	}
	if cfg.ReadHeaderTimeout != 2*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", cfg.ReadHeaderTimeout, 2*time.Second)
	}
	if cfg.ShutdownTimeout != 6*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 6*time.Second)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want normalized %q", cfg.LogLevel, "debug")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want normalized %q", cfg.LogFormat, "json")
	}
	if cfg.LogColor != "always" {
		t.Errorf("LogColor = %q, want normalized %q", cfg.LogColor, "always")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"bad duration", envReadTimeout, "ten seconds"},
		{"zero duration", envWriteTimeout, "0s"},
		{"bad log level", envLogLevel, "verbose"},
		{"bad log format", envLogFormat, "xml"},
		{"bad log color", envLogColor, "rainbow"},
		{"zero connect attempts", envDBConnectAttempts, "0"},
		{"bad boolean", envAutoMigrate, "sometimes"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(test.key, test.value)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want a validation error")
			}
		})
	}
}
