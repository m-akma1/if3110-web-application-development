package domain

import "time"

// UserManagementItem is one row of the admin user list.
type UserManagementItem struct {
	ID       string         `json:"id"`
	Username string         `json:"username"`
	Email    string         `json:"email,omitempty"`
	Role     string         `json:"role"` // PLATFORM_ADMIN, USER
	IsActive bool           `json:"is_active"`
	Groups   []GroupSummary `json:"groups"`
}

// MemberItem is a user listed under a group.
type MemberItem struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// GroupItem is one group in the admin console, with members and its grant per database.
type GroupItem struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	IsSystem    bool              `json:"is_system"`
	MemberCount int               `json:"member_count"`
	Members     []MemberItem      `json:"members"`
	Grants      map[string]string `json:"grants"` // database id -> VIEW_ONLY / VIEW_EDIT (missing = NONE)
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
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Host          string     `json:"host"`
	Port          int        `json:"port"`
	DBName        string     `json:"db_name"`
	Username      string     `json:"username"`
	SSLMode       string     `json:"ssl_mode"`
	Status        string     `json:"status"` // UNKNOWN, CONNECTED, ERROR
	LastError     string     `json:"last_error,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

// InviteCodeItem is one row of the admin invite code list.
type InviteCodeItem struct {
	Code      string    `json:"code"`
	GroupID   string    `json:"group_id"`
	GroupName string    `json:"group_name"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
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
