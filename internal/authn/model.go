package authn

import (
	"errors"
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

// DefaultSessionConfig returns the built-in session policy. config.Load overrides it from SESSION_* env vars.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		IdleTimeout:        30 * time.Minute,
		AbsoluteExpiration: 12 * time.Hour,
		RotationInterval:   15 * time.Minute,
		GracePeriod:        15 * time.Second,
	}
}

// ValidationResult menyimpan hasil validasi sesi beserta token baru apabila terjadi rotasi otomatis
type ValidationResult struct {
	User          *User // principal yang sudah terverifikasi (identitas saja; hak akses ditentukan rbac)
	Session       *Session
	Rotated       bool
	NewOpaqueID   string
	UsedGraceSlot bool
}
