package domain

// GroupSummary is a group the signed-in user belongs to.
type GroupSummary struct {
	ID       string
	Name     string
	IsSystem bool
}

// DatabaseAccessSummary is a database the signed-in user can open, with their effective level.
type DatabaseAccessSummary struct {
	DatabaseID      string
	DatabaseName    string
	EffectiveAccess string // "VIEW_ONLY" or "VIEW_EDIT"
}

// AuthLayoutUser is the view model every logged-in page receives.
// It is assembled by the HTTP layer from the identity (authn) plus groups and grants (rbac),
// and is display-only: authorization decisions always go through rbac.Authorizer.
type AuthLayoutUser struct {
	ID              string
	Username        string
	Email           string
	Role            string // "PLATFORM_ADMIN" or "USER"
	IsPlatformAdmin bool   // shows the Admin menu; grants no database access
	Groups          []GroupSummary
	Databases       []DatabaseAccessSummary
}
