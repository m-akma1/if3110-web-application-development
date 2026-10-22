package domain

type DBInstance struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DBMS      string `json:"dbms"` // POSTGRESQL, MYSQL, MARIADB
	Host      string `json:"host"`
	Port      int    `json:"port"`
	DBName    string `json:"db_name"`
	Status    string `json:"status"` // CONNECTED, ERROR
}

type UserManagementItem struct {
	ID        string   `json:"id"`
	Username  string   `json:"username"`
	Role      string   `json:"role"` // PLATFORM_ADMIN, USER
	AccessCount int    `json:"access_count"`
	Databases []string `json:"databases"`
}