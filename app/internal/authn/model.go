package authn

import "time"

// 1. User adalah model internal di Backend (berinteraksi dengan Database)
type User struct {
	ID           string    `json:"id"`            // Unique identifier seumur hidup untuk security principal
	Username     string    `json:"username"`      // Username untuk login
	PasswordHash string    `json:"-"`             // Tag json:"-" menjamin password hash TIDAK PERNAH ikut ter-serialize ke JSON response
	Role         string    `json:"role"`          // Contoh: "PLATFORM_ADMIN" atau "USER"
	CreatedAt    time.Time `json:"created_at"`
}

// 2. GroupSummary berisi informasi ringkas group yang diikuti oleh user
type GroupSummary struct {
	ID   string `json:"id"`   // Unique identifier untuk security group
	Name string `json:"name"` // Nama group (misal: "Backend-Engineers", "Data-Analysts")
}

// 3. DatabaseAccessSummary membantu Layout UI menampilkan daftar database beserta hak akses efektif user
// Catatan: Ini hanya untuk kebutuhan tampilan UI (seperti menyembunyikan tombol Edit).
// Pengecekan otorisasi sesungguhnya TETAP wajib dilakukan di backend pada setiap aksi!
type DatabaseAccessSummary struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DBMS            string `json:"dbms"`             // "POSTGRESQL", "MYSQL", atau "MARIADB"
	EffectiveAccess string `json:"effective_access"` // "VIEW_ONLY" atau "VIEW_EDIT"
}

// 4. AuthLayoutUser adalah struct yang dilempar ke Layout SSR Frontend (Response dari GET /api/auth/me)
type AuthLayoutUser struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	Role            string                  `json:"role"`              // "PLATFORM_ADMIN" atau "USER"
	IsPlatformAdmin bool                    `json:"is_platform_admin"` // Memudahkan layout menampilkan menu Admin (Kelola Koneksi DB & Group)
	Groups          []GroupSummary          `json:"groups"`            // Daftar group tempat user bergabung (1 atau lebih group)
	Databases       []DatabaseAccessSummary `json:"databases"`         // Daftar database yang boleh diakses beserta level akses efektifnya
}
