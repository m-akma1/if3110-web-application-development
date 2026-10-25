package authn

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
)

var (
	ErrInvalidUsername = errors.New("username harus 3-32 karakter: huruf kecil, angka, titik, garis bawah, atau tanda hubung")
	ErrInvalidEmail    = errors.New("format email tidak valid")
	ErrWeakPassword    = errors.New("password minimal 8 karakter dan maksimal 128 karakter")
	ErrUsernameTaken   = errors.New("username sudah terdaftar")
	ErrEmailTaken      = errors.New("email sudah terdaftar")
	ErrInvalidRole     = errors.New("role tidak valid")
)

const (
	MinPasswordLength = 8
	// MaxPasswordLength caps the work Argon2id does per request.
	MaxPasswordLength = 128
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

// NormalizeUsername trims and lowercases a username. Usernames are case-insensitive.
func NormalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizeEmail trims and lowercases an email address.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateUsername checks an already-normalized username.
func ValidateUsername(username string) error {
	if !usernameRe.MatchString(username) {
		return ErrInvalidUsername
	}
	return nil
}

// ValidateEmail checks an already-normalized email. An empty email is allowed (email is optional).
func ValidateEmail(email string) error {
	if email == "" {
		return nil
	}
	if len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return ErrInvalidEmail
	}
	return nil
}

// ValidatePassword checks password length. Length is counted in characters, not bytes.
func ValidatePassword(password string) error {
	n := len([]rune(password))
	if n < MinPasswordLength || n > MaxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

// ValidateRole accepts only the two platform roles.
func ValidateRole(role string) error {
	if role != RoleUser && role != RolePlatformAdmin {
		return ErrInvalidRole
	}
	return nil
}
