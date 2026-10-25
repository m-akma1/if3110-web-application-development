package domain

// GroupSummary is a group the signed-in user belongs to.
type GroupSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsSystem bool   `json:"is_system"`
}

// DatabaseAccessSummary is a database the signed-in user can open, with their effective level.
type DatabaseAccessSummary struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DBMS            string `json:"dbms"`             // "POSTGRESQL"
	EffectiveAccess string `json:"effective_access"` // "VIEW_ONLY" or "VIEW_EDIT"
}

// AuthLayoutUser is the view model every logged-in page receives (also GET /api/auth/me).
// It is assembled by the HTTP layer from the identity (authn) plus groups and grants (rbac),
// and is display-only: authorization decisions always go through rbac.Authorizer.
type AuthLayoutUser struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	Email           string                  `json:"email,omitempty"`
	Role            string                  `json:"role"`              // "PLATFORM_ADMIN" or "USER"
	IsPlatformAdmin bool                    `json:"is_platform_admin"` // shows the Admin menu; grants no database access
	Groups          []GroupSummary          `json:"groups"`
	Databases       []DatabaseAccessSummary `json:"databases"`
}

// AccessFor returns the user's effective level on dbID, or "NONE".
func (u *AuthLayoutUser) AccessFor(dbID string) string {
	if u == nil {
		return "NONE"
	}
	for _, db := range u.Databases {
		if db.DatabaseID == dbID {
			return db.EffectiveAccess
		}
	}
	return "NONE"
}

// CanEditDatabase reports whether the menu/buttons for creating and running migrations should show.
func (u *AuthLayoutUser) CanEditDatabase(dbID string) bool {
	return u.AccessFor(dbID) == "VIEW_EDIT"
}
