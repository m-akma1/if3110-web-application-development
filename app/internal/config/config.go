// Package config reads and validates the application's environment variables in one place.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration. Session timing lives in authn.DefaultSessionConfig.
type Config struct {
	DatabaseURL string // platform PostgreSQL (required)
	ListenAddr  string // LISTEN_ADDR, default ":8080"

	// Migration engine limits; zero keeps the engine defaults (migration.DefaultLimits).
	MigrationLockTimeout      time.Duration // MIGRATION_LOCK_TIMEOUT_MS: wait for a table lock on the target
	MigrationStatementTimeout time.Duration // MIGRATION_STATEMENT_TIMEOUT_MS: one statement's run time
	MigrationQueueTimeout     time.Duration // MIGRATION_QUEUE_TIMEOUT_MS: wait behind another migration on the same DB

	BootstrapAdminUsername string
	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	SeedDemoData     bool
	SeedDemoPassword string
	DemoTargetURL    string // DEMO_TARGET_URL: registered as "PostgreSQL Demo" by the demo seed

	// TargetCredentialsKey seals target database passwords (AES-256-GCM, base64 of 32 bytes).
	TargetCredentialsKey string
}

// Load reads the environment. It fails when a required value is missing or malformed.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:            strings.TrimSpace(getenv("DATABASE_URL")),
		ListenAddr:             strings.TrimSpace(getenv("LISTEN_ADDR")),
		BootstrapAdminUsername: strings.TrimSpace(getenv("BOOTSTRAP_ADMIN_USERNAME")),
		BootstrapAdminEmail:    strings.TrimSpace(getenv("BOOTSTRAP_ADMIN_EMAIL")),
		BootstrapAdminPassword: getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		SeedDemoPassword:       getenv("SEED_DEMO_PASSWORD"),
		DemoTargetURL:          strings.TrimSpace(getenv("DEMO_TARGET_URL")),
		TargetCredentialsKey:   strings.TrimSpace(getenv("TARGET_CREDENTIALS_KEY")),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	var errs []error
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required (the app no longer has an in-memory mode)"))
	}
	if raw := strings.TrimSpace(getenv("SEED_DEMO_DATA")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("SEED_DEMO_DATA must be true or false, got %q", raw))
		}
		cfg.SeedDemoData = v
	}
	if cfg.TargetCredentialsKey == "" {
		errs = append(errs, errors.New("TARGET_CREDENTIALS_KEY is required (generate one with: openssl rand -base64 32)"))
	}
	if cfg.SeedDemoData && cfg.SeedDemoPassword == "" {
		errs = append(errs, errors.New("SEED_DEMO_PASSWORD is required when SEED_DEMO_DATA=true"))
	}
	errs = append(errs, loadMillis(getenv, map[string]*time.Duration{
		"MIGRATION_LOCK_TIMEOUT_MS":      &cfg.MigrationLockTimeout,
		"MIGRATION_STATEMENT_TIMEOUT_MS": &cfg.MigrationStatementTimeout,
		"MIGRATION_QUEUE_TIMEOUT_MS":     &cfg.MigrationQueueTimeout,
	})...)
	return cfg, errors.Join(errs...)
}

// loadMillis reads optional positive millisecond values into durations.
func loadMillis(getenv func(string) string, dst map[string]*time.Duration) []error {
	var errs []error
	for name, d := range dst {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			continue
		}
		ms, err := strconv.Atoi(raw)
		if err != nil || ms <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a positive number of milliseconds, got %q", name, raw))
			continue
		}
		*d = time.Duration(ms) * time.Millisecond
	}
	return errs
}
