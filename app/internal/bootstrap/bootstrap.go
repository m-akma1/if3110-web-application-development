// Package bootstrap creates the first Platform Admins and optional demo data on startup.
// Everything here is insert-only: existing users are never overwritten, so restarts
// cannot reset passwords, roles or deactivations. The accounts and groups come from
// env vars (see internal/config); nothing is hardcoded here.
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

// ErrNoAdminConfigured means the platform has no active admin and BOOTSTRAP_ADMINS is empty.
var ErrNoAdminConfigured = errors.New("no active Platform Admin exists; set BOOTSTRAP_ADMINS " +
	`(e.g. [{"username":"admin","password":"..."}]) to create the first one`)

// Admin is one entry of BOOTSTRAP_ADMINS. Bootstrap admins are always PLATFORM_ADMIN.
type Admin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
}

// DemoUser is one entry of SEED_DEMO_USERS. An empty Role means USER.
type DemoUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Role     string `json:"role,omitempty"`
}

// DemoGroup is one entry of SEED_DEMO_GROUPS.
type DemoGroup struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	InviteCode  string   `json:"invite_code,omitempty"` // empty: no invite code
	Grant       string   `json:"grant,omitempty"`       // access level on the demo database; empty: no grant
	Members     []string `json:"members,omitempty"`     // usernames from SEED_DEMO_USERS
}

// EnsureAdmin creates every bootstrap admin when no active Platform Admin exists and returns
// the usernames it created. It does nothing once any active admin exists.
func EnsureAdmin(ctx context.Context, store *authn.SessionStore, admins []Admin) ([]string, error) {
	n, err := store.CountActiveAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: count admins: %w", err)
	}
	if n > 0 {
		return nil, nil
	}
	if len(admins) == 0 {
		return nil, ErrNoAdminConfigured
	}

	var created []string
	for _, a := range admins {
		u, err := store.RegisterUser(ctx, authn.RegisterInput{
			Username: a.Username,
			Email:    a.Email,
			Password: a.Password,
			Role:     authn.RolePlatformAdmin,
		})
		if errors.Is(err, authn.ErrUsernameTaken) || errors.Is(err, authn.ErrEmailTaken) {
			// Never promote or overwrite an existing account implicitly.
			return created, fmt.Errorf("bootstrap: %w: an existing account already uses the username or email of "+
				"BOOTSTRAP_ADMINS entry %q but there is no active admin; pick a different username", err, a.Username)
		}
		if err != nil {
			return created, fmt.Errorf("bootstrap: create admin %s: %w", a.Username, err)
		}
		created = append(created, u.Username)
	}
	return created, nil
}

// SeedDemo inserts demo users and groups that do not exist yet and returns what it created.
// It never re-adds anything an admin removed: memberships are only added for users created in
// this run, and invite codes only for groups created in this run. groupRepo may be nil (users only).
func SeedDemo(ctx context.Context, store *authn.SessionStore, groupRepo *groups.Repository, users []DemoUser, demoGroups []DemoGroup) ([]string, error) {
	var created []string
	newUserIDs := map[string]string{} // username -> id, only users created now
	for _, du := range users {
		role := du.Role
		if role == "" {
			role = authn.RoleUser
		}
		user, err := store.RegisterUser(ctx, authn.RegisterInput{
			Username: du.Username,
			Email:    du.Email,
			Password: du.Password,
			Role:     role,
		})
		if errors.Is(err, authn.ErrUsernameTaken) || errors.Is(err, authn.ErrEmailTaken) {
			continue
		}
		if err != nil {
			return created, fmt.Errorf("bootstrap: seed %s: %w", du.Username, err)
		}
		newUserIDs[user.Username] = user.ID
		created = append(created, "user "+user.Username)
	}
	if groupRepo == nil {
		return created, nil
	}

	for _, dg := range demoGroups {
		g, err := groupRepo.GetByName(ctx, dg.Name)
		switch {
		case errors.Is(err, groups.ErrGroupNotFound):
			g, err = groupRepo.Create(ctx, dg.Name, dg.Description)
			if err != nil {
				return created, fmt.Errorf("bootstrap: seed group %s: %w", dg.Name, err)
			}
			created = append(created, "group "+dg.Name)
			if dg.InviteCode != "" {
				if _, err := groupRepo.CreateInviteCode(ctx, dg.InviteCode, g.ID, ""); err != nil && !errors.Is(err, groups.ErrInviteCodeTaken) {
					return created, fmt.Errorf("bootstrap: seed invite code for %s: %w", dg.Name, err)
				}
			}
		case err != nil:
			return created, fmt.Errorf("bootstrap: look up group %s: %w", dg.Name, err)
		}
		for _, username := range dg.Members {
			if id, ok := newUserIDs[authn.NormalizeUsername(username)]; ok {
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

// SeedDemoTarget registers the demo database (once, insert-only) under DemoTargetName and gives each
// demo group its Grant on it. If a target with DemoTargetName already exists, nothing changes,
// so an admin's edits, grant changes or deletion of the demo groups are never undone.
func SeedDemoTarget(ctx context.Context, targets *dbmanager.Manager, grants *rbac.PostgresGrantStore, groupRepo *groups.Repository, in dbmanager.TargetInput, demoGroups []DemoGroup) (bool, error) {
	in.Name = DemoTargetName
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
	for _, dg := range demoGroups {
		if dg.Grant == "" {
			continue
		}
		g, err := groupRepo.GetByName(ctx, dg.Name)
		if errors.Is(err, groups.ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return true, err
		}
		if _, err := grants.SetGroupGrant(ctx, g.ID, t.ID, rbac.AccessLevel(dg.Grant)); err != nil {
			return true, fmt.Errorf("bootstrap: grant %s on demo target: %w", dg.Name, err)
		}
	}
	return true, nil
}
