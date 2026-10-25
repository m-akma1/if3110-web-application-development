// Package config reads and validates the application's environment variables in one place.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the process configuration. Session timing lives in authn.DefaultSessionConfig.
type Config struct {
	DatabaseURL string // platform PostgreSQL (required)
	ListenAddr  string // LISTEN_ADDR, default ":8080"

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
	return cfg, errors.Join(errs...)
}
