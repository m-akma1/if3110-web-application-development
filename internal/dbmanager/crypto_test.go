package dbmanager_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"dbplatform/internal/dbmanager"
)

func testKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestCredentialCipher_RoundTrip(t *testing.T) {
	c, err := dbmanager.NewCredentialCipher(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal("s3cr3t-p@ss", "target-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "s3cr3t-p@ss" || len(sealed) <= len("s3cr3t-p@ss") {
		t.Fatalf("password tidak boleh tersimpan dalam bentuk plaintext")
	}
	again, _ := c.Seal("s3cr3t-p@ss", "target-1")
	if string(again) == string(sealed) {
		t.Fatalf("setiap seal harus memakai nonce acak yang berbeda")
	}
	plain, err := c.Open(sealed, "target-1")
	if err != nil || plain != "s3cr3t-p@ss" {
		t.Fatalf("round-trip gagal: %q, %v", plain, err)
	}
}

func TestCredentialCipher_RejectsTamperingWrongKeyAndWrongTarget(t *testing.T) {
	c, _ := dbmanager.NewCredentialCipher(testKey(t))
	other, _ := dbmanager.NewCredentialCipher(testKey(t))
	sealed, _ := c.Seal("password", "target-1")

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01
	cases := map[string]func() (string, error){
		"data diubah":    func() (string, error) { return c.Open(tampered, "target-1") },
		"kunci lain":     func() (string, error) { return other.Open(sealed, "target-1") },
		"target lain":    func() (string, error) { return c.Open(sealed, "target-2") },
		"terlalu pendek": func() (string, error) { return c.Open([]byte{1, 2, 3}, "target-1") },
	}
	for name, open := range cases {
		if _, err := open(); !errors.Is(err, dbmanager.ErrDecrypt) {
			t.Errorf("%s: seharusnya ErrDecrypt, dapat %v", name, err)
		}
	}
}

func TestNewCredentialCipher_ValidatesKey(t *testing.T) {
	for _, k := range []string{"", "bukan-base64!!", base64.StdEncoding.EncodeToString([]byte("terlalu-pendek"))} {
		if _, err := dbmanager.NewCredentialCipher(k); !errors.Is(err, dbmanager.ErrInvalidCredentialsKey) {
			t.Errorf("kunci %q seharusnya ditolak, dapat %v", k, err)
		}
	}
}
