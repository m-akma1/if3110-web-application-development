package rbac

import (
	"context"
	"errors"
	"sort"

	"dbplatform/internal/ids"
)

// ErrForbidden means the user may not perform the action on that database.
// Unknown databases also return ErrForbidden, so callers cannot probe which ids exist.
var ErrForbidden = errors.New("akses ditolak")

// Grant is one group's grant on one database, as seen through one of the user's memberships.
type Grant struct {
	DatabaseID   string
	DatabaseName string
	Level        AccessLevel
}

// GrantStore loads raw grants. It does not decide anything; the rules stay in this package.
type GrantStore interface {
	// LevelsForUser returns the level of every grant on dbID held by any group the user belongs to.
	LevelsForUser(ctx context.Context, userID, dbID string) ([]AccessLevel, error)
	// GrantsForUser returns every grant held by any group the user belongs to (one row per group and database).
	GrantsForUser(ctx context.Context, userID string) ([]Grant, error)
}

// DatabaseAccess is a database the user can see, with their effective level on it.
type DatabaseAccess struct {
	DatabaseID   string
	DatabaseName string
	Level        AccessLevel
}

// Authorizer answers "may user X do action Y on database Z" from group grants.
// Platform Admin is not an access level: admins only reach databases through their groups.
type Authorizer struct {
	store GrantStore
}

func NewAuthorizer(store GrantStore) *Authorizer {
	return &Authorizer{store: store}
}

// EffectiveLevel returns the user's effective level on dbID (NONE for unknown databases).
func (a *Authorizer) EffectiveLevel(ctx context.Context, userID, dbID string) (AccessLevel, error) {
	if !ids.IsValid(userID) || !ids.IsValid(dbID) {
		return AccessNone, nil
	}
	levels, err := a.store.LevelsForUser(ctx, userID, dbID)
	if err != nil {
		return AccessNone, err
	}
	return Effective(levels...), nil
}

// Authorize returns the effective level and nil if the action is allowed, or ErrForbidden otherwise.
// Any storage error is returned as is and must be treated as a denial.
func (a *Authorizer) Authorize(ctx context.Context, userID, dbID string, action Action) (AccessLevel, error) {
	level, err := a.EffectiveLevel(ctx, userID, dbID)
	if err != nil {
		return AccessNone, err
	}
	if !IsAllowed(level, action) {
		return level, ErrForbidden
	}
	return level, nil
}

// AccessibleDatabases lists every database where the user's effective level is above NONE, sorted by name.
func (a *Authorizer) AccessibleDatabases(ctx context.Context, userID string) ([]DatabaseAccess, error) {
	if !ids.IsValid(userID) {
		return nil, nil
	}
	grants, err := a.store.GrantsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	byID := map[string]*DatabaseAccess{}
	for _, g := range grants {
		acc, ok := byID[g.DatabaseID]
		if !ok {
			acc = &DatabaseAccess{DatabaseID: g.DatabaseID, DatabaseName: g.DatabaseName, Level: AccessNone}
			byID[g.DatabaseID] = acc
		}
		acc.Level = Max(acc.Level, g.Level)
	}
	list := make([]DatabaseAccess, 0, len(byID))
	for _, acc := range byID {
		if acc.Level != AccessNone {
			list = append(list, *acc)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].DatabaseName != list[j].DatabaseName {
			return list[i].DatabaseName < list[j].DatabaseName
		}
		return list[i].DatabaseID < list[j].DatabaseID
	})
	return list, nil
}
