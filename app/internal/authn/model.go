package authn

import (
	"errors"
	"os"
	"strconv"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("username atau password tidak valid")
	ErrSessionNotFound    = errors.New("sesi tidak ditemukan")
	ErrSessionExpired     = errors.New("sesi telah berakhir (idle atau absolute expiration)")
	ErrSessionRevoked     = errors.New("sesi telah dinonaktifkan")
	ErrUserInactive       = errors.New("akun pengguna tidak aktif")
	ErrUserNotFound       = errors.New("pengguna tidak ditemukan")
)

// User adalah model internal di Backend
type User struct {
	ID           string    `json:"id"`              // UUID seumur hidup untuk security principal
	Username     string    `json:"username"`        // Username unik (huruf kecil) untuk login / tampilan
	Email        string    `json:"email,omitempty"` // Email opsional; jika diisi juga dapat dipakai untuk login
	PasswordHash string    `json:"-"`               // Tag json:"-" menjamin password hash TIDAK PERNAH ikut ter-serialize ke JSON response
	Role         string    `json:"role"`            // Contoh: "PLATFORM_ADMIN" atau "USER"
	IsActive     bool      `json:"is_active"`       // Status aktif security principal
	CreatedAt    time.Time `json:"created_at"`
}

// GroupSummary berisi informasi ringkas group yang diikuti oleh user
type GroupSummary struct {
	ID   string `json:"id"`   // Unique identifier untuk security group
	Name string `json:"name"` // Nama group (misal: "Backend-Engineers", "Data-Analysts")
}

// DatabaseAccessSummary membantu Layout UI menampilkan daftar database beserta hak akses efektif user
type DatabaseAccessSummary struct {
	DatabaseID      string `json:"database_id"`
	DatabaseName    string `json:"database_name"`
	DBMS            string `json:"dbms"`             // "POSTGRESQL", "MYSQL", atau "MARIADB"
	EffectiveAccess string `json:"effective_access"` // "VIEW_ONLY" atau "VIEW_EDIT"
}

// AuthLayoutUser adalah struct yang dilempar ke Layout SSR Frontend (Response dari GET /api/auth/me)
type AuthLayoutUser struct {
	ID              string                  `json:"id"`
	Username        string                  `json:"username"`
	Email           string                  `json:"email"`
	Role            string                  `json:"role"`              // "PLATFORM_ADMIN" atau "USER"
	IsPlatformAdmin bool                    `json:"is_platform_admin"` // Memudahkan layout menampilkan menu Admin (Kelola Koneksi DB & Group)
	Groups          []GroupSummary          `json:"groups"`            // Daftar group tempat user bergabung (1 atau lebih group)
	Databases       []DatabaseAccessSummary `json:"databases"`         // Daftar database yang boleh diakses beserta level akses efektifnya
}

// Session merepresentasikan state siklus hidup sesi di PostgreSQL Internal
type Session struct {
	ID                   string     // Unique identifier untuk baris sesi (session family ID)
	UserID               string     // Unique identifier permanen milik pengguna (User.ID)
	TokenHash            string     // SHA-256 hash dari opaque session ID aktif
	PreviousTokenHash    string     // SHA-256 hash dari opaque session ID sebelum rotasi
	GracePeriodExpiresAt *time.Time // Batas waktu penerimaan PreviousTokenHash untuk request paralel
	CreatedAt            time.Time  // Waktu sesi pertama kali dibuat saat login
	LastActivityAt       time.Time  // Waktu aktivitas terakhir
	LastRotatedAt        time.Time  // Waktu terakhir kali session ID dirotasi
	IdleExpiresAt        time.Time  // Batas waktu idle timeout
	AbsoluteExpiresAt    time.Time  // Batas waktu berakhir mutlak (tidak pernah diperpanjang saat rotasi/aktivitas)
	IsRevoked            bool       // Flag penonaktifan sesi seketika
}

// SessionConfig mendefinisikan parameter kebijakan siklus hidup sesi
type SessionConfig struct {
	IdleTimeout        time.Duration
	AbsoluteExpiration time.Duration
	RotationInterval   time.Duration
	GracePeriod        time.Duration
}

// DefaultSessionConfig memuat konfigurasi dari environment variable (dengan nilai fallback standar)
func DefaultSessionConfig() SessionConfig {
	idleMin := getEnvInt("SESSION_IDLE_TIMEOUT_MINUTES", 30)
	absHours := getEnvInt("SESSION_ABSOLUTE_EXPIRATION_HOURS", 12)
	rotMin := getEnvInt("SESSION_ROTATION_INTERVAL_MINUTES", 15)
	graceSec := getEnvInt("SESSION_GRACE_PERIOD_SECONDS", 15)

	return SessionConfig{
		IdleTimeout:        time.Duration(idleMin) * time.Minute,
		AbsoluteExpiration: time.Duration(absHours) * time.Hour,
		RotationInterval:   time.Duration(rotMin) * time.Minute,
		GracePeriod:        time.Duration(graceSec) * time.Second,
	}
}

func getEnvInt(key string, fallback int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	v, err := strconv.Atoi(valStr)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// ValidationResult menyimpan hasil validasi sesi beserta token baru apabila terjadi rotasi otomatis
type ValidationResult struct {
	User          *AuthLayoutUser
	Session       *Session
	Rotated       bool
	NewOpaqueID   string
	UsedGraceSlot bool
}
