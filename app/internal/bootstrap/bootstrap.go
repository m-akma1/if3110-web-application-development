// Package bootstrap creates the first Platform Admin and optional demo data on startup.
// Everything here is insert-only: existing users are never overwritten, so restarts
// cannot reset passwords, roles or deactivations.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/rbac"
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
	{Username: "akmal", Email: "akmal@example.com", Role: authn.RoleUser},
	{Username: "dzaki", Role: authn.RoleUser}, // no email: logs in by username only
}

// DemoGroup is a demo group with its invite code and the demo users placed in it.
type DemoGroup struct {
	Name        string
	Description string
	InviteCode  string
	Members     []string // usernames from DemoUsers
	DemoGrant   string   // level on the demo target database (see SeedDemoTarget)
}

// DemoGroups are created by SeedDemo when a groups repository is given.
var DemoGroups = []DemoGroup{
	{Name: "Engineers", Description: "Demo: tim yang boleh membuat dan menjalankan migrasi.", InviteCode: "ENGINEERS-DEMO", Members: []string{"akmal"}, DemoGrant: "VIEW_EDIT"},
	{Name: "Analysts", Description: "Demo: tim yang hanya melihat schema dan data.", InviteCode: "ANALYSTS-DEMO", Members: []string{"dzaki"}, DemoGrant: "VIEW_ONLY"},
}

// SeedDemo inserts demo data that does not exist yet and returns what it created.
// It never re-adds anything an admin removed: memberships are only added for users created in
// this run, and invite codes only for groups created in this run. groupRepo may be nil (users only).
func SeedDemo(ctx context.Context, store *authn.SessionStore, groupRepo *groups.Repository, cfg DemoConfig) ([]string, error) {
	if cfg.Password == "" {
		return nil, errors.New("bootstrap: SEED_DEMO_PASSWORD must be set when SEED_DEMO_DATA=true")
	}
	var created []string
	newUserIDs := map[string]string{} // username -> id, only users created now
	for _, u := range DemoUsers {
		u.Password = cfg.Password
		user, err := store.RegisterUser(ctx, u)
		if errors.Is(err, authn.ErrUsernameTaken) || errors.Is(err, authn.ErrEmailTaken) {
			continue
		}
		if err != nil {
			return created, fmt.Errorf("bootstrap: seed %s: %w", u.Username, err)
		}
		newUserIDs[user.Username] = user.ID
		created = append(created, "user "+u.Username)
	}
	if groupRepo == nil {
		return created, nil
	}

	for _, dg := range DemoGroups {
		g, err := groupRepo.GetByName(ctx, dg.Name)
		switch {
		case errors.Is(err, groups.ErrGroupNotFound):
			g, err = groupRepo.Create(ctx, dg.Name, dg.Description)
			if err != nil {
				return created, fmt.Errorf("bootstrap: seed group %s: %w", dg.Name, err)
			}
			created = append(created, "group "+dg.Name)
			if _, err := groupRepo.CreateInviteCode(ctx, dg.InviteCode, g.ID, ""); err != nil && !errors.Is(err, groups.ErrInviteCodeTaken) {
				return created, fmt.Errorf("bootstrap: seed invite code %s: %w", dg.InviteCode, err)
			}
		case err != nil:
			return created, fmt.Errorf("bootstrap: look up group %s: %w", dg.Name, err)
		}
		for _, username := range dg.Members {
			if id, ok := newUserIDs[username]; ok {
				if err := groupRepo.AddMember(ctx, g.ID, id); err != nil {
					return created, fmt.Errorf("bootstrap: add %s to %s: %w", username, dg.Name, err)
				}
			}
		}
	}
	return created, nil
}

// DemoTargetName is the display name of the bundled demo database.
const DemoTargetName = "PostgreSQL Demo"

// SeedDemoTarget registers the demo database from DEMO_TARGET_URL (once, insert-only) and gives the
// demo groups their DemoGrant on it. If a target with DemoTargetName already exists, nothing changes,
// so an admin's edits, grant changes or deletion of the demo groups are never undone.
func SeedDemoTarget(ctx context.Context, targets *dbmanager.Manager, grants *rbac.PostgresGrantStore, groupRepo *groups.Repository, demoURL string) (bool, error) {
	if demoURL == "" {
		return false, nil
	}
	in, err := dbmanager.TargetInputFromURL(DemoTargetName, demoURL)
	if err != nil {
		return false, fmt.Errorf("bootstrap: DEMO_TARGET_URL: %w", err)
	}
	existing, err := targets.List(ctx)
	if err != nil {
		return false, err
	}
	for _, t := range existing {
		if strings.EqualFold(t.Name, DemoTargetName) {
			return false, nil
		}
	}

	t, err := targets.Create(ctx, in)
	if err != nil {
		return false, fmt.Errorf("bootstrap: register demo target: %w", err)
	}
	for _, dg := range DemoGroups {
		if dg.DemoGrant == "" {
			continue
		}
		g, err := groupRepo.GetByName(ctx, dg.Name)
		if errors.Is(err, groups.ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return true, err
		}
		if _, err := grants.SetGroupGrant(ctx, g.ID, t.ID, rbac.AccessLevel(dg.DemoGrant)); err != nil {
			return true, fmt.Errorf("bootstrap: grant %s on demo target: %w", dg.Name, err)
		}
	}
	return true, nil
}
