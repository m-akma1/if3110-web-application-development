package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64 MB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen uint32 = 16
)

// dummyPasswordHash digunakan untuk mencegah username enumeration melalui timing attack
var dummyPasswordHash string

func init() {
	h, err := HashPassword("dummy-password-for-constant-time-mitigation")
	if err == nil {
		dummyPasswordHash = h
	}
}

// HashPassword menghasilkan hash Argon2id dengan format standar PHC:
// $argon2id$v=19$m=65536,t=1,p=4$<base64-salt>$<base64-hash>
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password tidak boleh kosong")
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gagal membuat salt acak: %w", err)
	}

	hash := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Hash)

	return encoded, nil
}

// VerifyPassword memverifikasi plain password terhadap encoded hash Argon2id secara constant-time
func VerifyPassword(plain, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("format hash password tidak valid")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("versi argon2 tidak valid: %w", err)
	}

	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("parameter argon2 tidak valid: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("gagal decode salt: %w", err)
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("gagal decode hash: %w", err)
	}

	computedHash := argon2.IDKey([]byte(plain), salt, timeCost, memory, threads, uint32(len(expectedHash)))

	if subtle.ConstantTimeCompare(computedHash, expectedHash) == 1 {
		return true, nil
	}
	return false, nil
}

// VerifyDummyPassword menjalankan kalkulasi Argon2id palsu saat username tidak ditemukan
// agar waktu eksekusi tetap seragam (mencegah timing side-channel).
func VerifyDummyPassword(plain string) {
	if dummyPasswordHash != "" {
		_, _ = VerifyPassword(plain, dummyPasswordHash)
	}
}

// GenerateOpaqueToken menghasilkan opaque session identifier acak 256-bit (32 byte CSPRNG)
func GenerateOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gagal membuat opaque session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashOpaqueToken menghasilkan SHA-256 hex digest (64 karakter) dari opaque session token
// untuk disimpan di PostgreSQL Internal tanpa mengekspos token mentah.
func HashOpaqueToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}
