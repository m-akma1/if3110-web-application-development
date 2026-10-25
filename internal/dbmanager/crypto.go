package dbmanager

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCredentialsKey means TARGET_CREDENTIALS_KEY is missing or not 32 bytes of base64.
var ErrInvalidCredentialsKey = errors.New("TARGET_CREDENTIALS_KEY must be 32 random bytes encoded as base64 (generate one with: openssl rand -base64 32)")

// ErrDecrypt means a sealed credential could not be opened (wrong key or tampered data).
var ErrDecrypt = errors.New("gagal membuka kredensial database target (kunci salah atau data rusak)")

// CredentialCipher seals target database passwords with AES-256-GCM before they are stored.
// Sealed format: nonce (12 bytes) || ciphertext+tag.
type CredentialCipher struct {
	aead cipher.AEAD
}

// NewCredentialCipher parses a base64 32-byte key.
func NewCredentialCipher(base64Key string) (*CredentialCipher, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Key))
	if err != nil || len(key) != 32 {
		return nil, ErrInvalidCredentialsKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("dbmanager: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("dbmanager: %w", err)
	}
	return &CredentialCipher{aead: aead}, nil
}

// Seal encrypts plaintext. targetID is bound as additional data, so a sealed password
// copied onto another target row will not open.
func (c *CredentialCipher) Seal(plaintext, targetID string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(targetID)), nil
}

// Open decrypts a value produced by Seal for the same targetID.
func (c *CredentialCipher) Open(sealed []byte, targetID string) (string, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n+c.aead.Overhead() {
		return "", ErrDecrypt
	}
	plain, err := c.aead.Open(nil, sealed[:n], sealed[n:], []byte(targetID))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
