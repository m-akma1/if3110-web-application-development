package authn

// GroupSummary berisi informasi ringkas group yang diikuti oleh user
type GroupSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DatabaseAccessSummary membantu Layout UI menampilkan daftar database beserta hak akses efektif user
type DatabaseAccessSummary struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DBMS            string `json:"dbms"`
	EffectiveAccess string `json:"effective_access"`
}

// AuthLayoutUser adalah struct yang dilempar ke Layout SSR Frontend (Response dari GET /api/auth/me)
type AuthLayoutUser struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	Role            string                  `json:"role"`
	IsPlatformAdmin bool                    `json:"is_platform_admin"`
	Groups          []GroupSummary          `json:"groups"`
	Databases       []DatabaseAccessSummary `json:"databases"`
}
