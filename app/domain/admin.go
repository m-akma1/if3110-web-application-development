package domain

type DBInstance struct {
	ID         string `json:"id"`
	InviteCode string `json:"invite_code"`
	Name       string `json:"name"`
	DBMS       string `json:"dbms"` // POSTGRESQL, MYSQL, MARIADB
	Host       string `json:"host"`
	Port       int    `json:"port"`
	DBName     string `json:"db_name"`
	Status     string `json:"status"` // CONNECTED, ERROR
}

type TeamInviteCodeItem struct {
	Code            string `json:"code"`
	GroupName       string `json:"group_name"`
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DefaultAccess   string `json:"default_access"` // VIEW_ONLY, VIEW_EDIT
}

type UserAccessGrant struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DBMS            string `json:"dbms"`
	EffectiveAccess string `json:"effective_access"` // VIEW_ONLY, VIEW_EDIT, NONE
}

type UserManagementItem struct {
	ID        string            `json:"id"`
	Username  string            `json:"username"`
	Email     string            `json:"email"`
	Role      string            `json:"role"` // PLATFORM_ADMIN, USER
	GroupName string            `json:"group_name"`
	IsActive  bool              `json:"is_active"`
	Grants    []UserAccessGrant `json:"grants"`
}
