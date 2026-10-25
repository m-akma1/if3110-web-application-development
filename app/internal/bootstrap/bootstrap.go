// Package bootstrap creates the first Platform Admin and optional demo data on startup.
// Everything here is insert-only: existing users are never overwritten, so restarts
// cannot reset passwords, roles or deactivations.
package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/app/internal/authn"
)

// ErrNoAdminConfigured means the platform has no active admin and BOOTSTRAP_ADMIN_* is not set.
var ErrNoAdminConfigured = errors.New("no active Platform Admin exists; set BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD " +
	"(BOOTSTRAP_ADMIN_EMAIL is optional) to create the first one")

// AdminConfig is the first admin's account, read from BOOTSTRAP_ADMIN_* env vars.
type AdminConfig struct {
	Username string
	Email    string
	Password string
}

// EnsureAdmin creates the bootstrap admin when no active Platform Admin exists.
// It does nothing (and returns false) once any active admin exists.
func EnsureAdmin(ctx context.Context, store *authn.SessionStore, cfg AdminConfig) (bool, error) {
	n, err := store.CountActiveAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("bootstrap: count admins: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	if cfg.Username == "" || cfg.Password == "" {
		return false, ErrNoAdminConfigured
	}

	_, err = store.RegisterUser(ctx, authn.RegisterInput{
		Username: cfg.Username,
		Email:    cfg.Email,
		Password: cfg.Password,
		Role:     authn.RolePlatformAdmin,
	})
	if errors.Is(err, authn.ErrUsernameTaken) || errors.Is(err, authn.ErrEmailTaken) {
		// Never promote or overwrite an existing account implicitly.
		return false, fmt.Errorf("bootstrap: %w: an existing account already uses BOOTSTRAP_ADMIN_USERNAME/EMAIL "+
			"but there is no active admin; pick a different bootstrap username", err)
	}
	if err != nil {
		return false, fmt.Errorf("bootstrap: create admin: %w", err)
	}
	return true, nil
}

// DemoConfig controls SEED_DEMO_DATA.
type DemoConfig struct {
	Password string // shared password for every demo account (SEED_DEMO_PASSWORD)
}

// DemoUsers are the demo accounts created by SeedDemo.
var DemoUsers = []authn.RegisterInput{
	{Username: "moreno", Email: "moreno@example.com", Role: authn.RolePlatformAdmin},
	{Username: "dhia", Email: "dhia@example.com", Role: authn.RoleUser},
	{Username: "caca", Role: authn.RoleUser}, // no email: logs in by username only
}

// SeedDemo inserts the demo accounts that do not exist yet and returns the usernames it created.
func SeedDemo(ctx context.Context, store *authn.SessionStore, cfg DemoConfig) ([]string, error) {
	if cfg.Password == "" {
		return nil, errors.New("bootstrap: SEED_DEMO_PASSWORD must be set when SEED_DEMO_DATA=true")
	}
	var created []string
	for _, u := range DemoUsers {
		u.Password = cfg.Password
		_, err := store.RegisterUser(ctx, u)
		if errors.Is(err, authn.ErrUsernameTaken) || errors.Is(err, authn.ErrEmailTaken) {
			continue
		}
		if err != nil {
			return created, fmt.Errorf("bootstrap: seed %s: %w", u.Username, err)
		}
		created = append(created, u.Username)
	}
	return created, nil
}
