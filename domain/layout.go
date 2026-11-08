package domain

type GroupSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsSystem bool   `json:"is_system"`
}

type DatabaseAccessSummary struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	EffectiveAccess string `json:"effective_access"`
}

type AuthLayoutUser struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	Email           string                  `json:"email"`
	Role            string                  `json:"role"`
	IsPlatformAdmin bool                    `json:"is_platform_admin"`
	Groups          []GroupSummary          `json:"groups"`
	Databases       []DatabaseAccessSummary `json:"databases"`
}
