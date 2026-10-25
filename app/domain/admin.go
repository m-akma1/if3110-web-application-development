package domain

import "time"

// UserManagementItem is one row of the admin user list.
type UserManagementItem struct {
	ID       string
	Username string
	Email    string
	Role     string // PLATFORM_ADMIN, USER
	IsActive bool
	Groups   []GroupSummary
}

// MemberItem is a user listed under a group.
type MemberItem struct {
	ID       string
	Username string
}

// GroupItem is one group in the admin console, with members and its grant per database.
type GroupItem struct {
	ID          string
	Name        string
	Description string
	IsSystem    bool
	MemberCount int
	Members     []MemberItem
	Grants      map[string]string // database id -> VIEW_ONLY / VIEW_EDIT (missing = NONE)
}

// GrantFor returns the group's level on a database, or "NONE".
func (g GroupItem) GrantFor(databaseID string) string {
	if lvl, ok := g.Grants[databaseID]; ok {
		return lvl
	}
	return "NONE"
}

// HasMember reports whether a user is in the group.
func (g GroupItem) HasMember(userID string) bool {
	for _, m := range g.Members {
		if m.ID == userID {
			return true
		}
	}
	return false
}

// TargetItem is a registered target database (never includes the password).
type TargetItem struct {
	ID            string
	Name          string
	Host          string
	Port          int
	DBName        string
	Username      string
	SSLMode       string
	Status        string // UNKNOWN, CONNECTED, ERROR
	LastError     string
	LastCheckedAt *time.Time
}

// InviteCodeItem is one row of the admin invite code list.
type InviteCodeItem struct {
	Code      string
	GroupID   string
	GroupName string
	IsActive  bool
	CreatedAt time.Time
}

// AdminOverview is everything the admin console shows.
type AdminOverview struct {
	Users   []UserManagementItem
	Groups  []GroupItem
	Targets []TargetItem
	Codes   []InviteCodeItem
}

// Flash is a one-off success or error message shown above an admin section.
type Flash struct {
	Success bool
	Message string
}
