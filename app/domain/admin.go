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

// GroupItem is one row of the admin group list.
type GroupItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsSystem    bool   `json:"is_system"`
	MemberCount int    `json:"member_count"`
}

// InviteCodeItem is one row of the admin invite code list.
type InviteCodeItem struct {
	Code      string    `json:"code"`
	GroupName string    `json:"group_name"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}
