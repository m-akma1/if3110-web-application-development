package authn_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/authn"
)

func TestValidateUsername(t *testing.T) {
	valid := []string{"abc", "budi.santoso", "user_01", "a-b-c", "x23456789012345678901234567890xy"}
	invalid := []string{"", "ab", "Budi", "budi santoso", "budi@mail", "x234567890123456789012345678901xy", "ümlaut"}
	for _, u := range valid {
		if err := authn.ValidateUsername(u); err != nil {
			t.Errorf("username %q seharusnya valid: %v", u, err)
		}
	}
	for _, u := range invalid {
		if err := authn.ValidateUsername(u); err == nil {
			t.Errorf("username %q seharusnya ditolak", u)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	for _, e := range []string{"", "a@b.co", "budi.santoso@example.com"} {
		if err := authn.ValidateEmail(e); err != nil {
			t.Errorf("email %q seharusnya valid: %v", e, err)
		}
	}
	for _, e := range []string{"budi", "budi@", "@example.com", "budi@localhost", "Budi <budi@example.com>", "a b@example.com"} {
		if err := authn.ValidateEmail(e); err == nil {
			t.Errorf("email %q seharusnya ditolak", e)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := authn.ValidatePassword("1234567"); err == nil {
		t.Errorf("password 7 karakter seharusnya ditolak")
	}
	if err := authn.ValidatePassword("12345678"); err != nil {
		t.Errorf("password 8 karakter seharusnya diterima: %v", err)
	}
	long := make([]rune, authn.MaxPasswordLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := authn.ValidatePassword(string(long)); err == nil {
		t.Errorf("password lebih dari %d karakter seharusnya ditolak", authn.MaxPasswordLength)
	}
}

func TestRegisterUser_NormalizesAndRejectsDuplicates(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()

	u, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "  Budi.Santoso ", Email: " Budi@Example.COM ", Password: "PasswordBudi123"})
	if err != nil {
		t.Fatalf("registrasi seharusnya berhasil: %v", err)
	}
	if u.Username != "budi.santoso" || u.Email != "budi@example.com" {
		t.Fatalf("username/email seharusnya dinormalisasi ke huruf kecil, dapat %q / %q", u.Username, u.Email)
	}

	cases := []struct {
		name string
		in   authn.RegisterInput
		want error
	}{
		{"username sama beda huruf", authn.RegisterInput{Username: "BUDI.SANTOSO", Password: "PasswordLain123"}, authn.ErrUsernameTaken},
		{"email sama beda huruf", authn.RegisterInput{Username: "budi2", Email: "BUDI@example.com", Password: "PasswordLain123"}, authn.ErrEmailTaken},
		{"username tidak valid", authn.RegisterInput{Username: "b!", Password: "PasswordLain123"}, authn.ErrInvalidUsername},
		{"email tidak valid", authn.RegisterInput{Username: "budi3", Email: "bukan-email", Password: "PasswordLain123"}, authn.ErrInvalidEmail},
		{"password lemah", authn.RegisterInput{Username: "budi4", Password: "pendek"}, authn.ErrWeakPassword},
		{"role tidak valid", authn.RegisterInput{Username: "budi5", Password: "PasswordLain123", Role: "SUPERUSER"}, authn.ErrInvalidRole},
	}
	for _, tc := range cases {
		if _, err := store.RegisterUser(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: ingin %v, dapat %v", tc.name, tc.want, err)
		}
	}
}

func TestRegisterUser_EmailIsOptional(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()

	for _, name := range []string{"tanpa.email.1", "tanpa.email.2"} {
		if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: name, Password: "PasswordTanpaEmail1"}); err != nil {
			t.Fatalf("beberapa user tanpa email seharusnya boleh: %v", err)
		}
	}
}

func TestLogin_ByUsernameOrEmail(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "sinta", Email: "sinta@example.com", Password: "PasswordSinta123"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "joko", Password: "PasswordJoko123"}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"sinta", "SINTA", "sinta@example.com", "Sinta@Example.com", "joko"} {
		if _, _, err := store.Login(ctx, id, map[bool]string{true: "PasswordJoko123", false: "PasswordSinta123"}[id == "joko"], ""); err != nil {
			t.Errorf("login dengan %q seharusnya berhasil: %v", id, err)
		}
	}

	// Tidak ada tebakan local-part: "sinta@lain.com" bukan email milik sinta
	for _, id := range []string{"sinta@lain.com", "joko@email.com", "tidak.ada"} {
		if _, _, err := store.Login(ctx, id, "PasswordSinta123", ""); !errors.Is(err, authn.ErrInvalidCredentials) {
			t.Errorf("login dengan %q seharusnya ditolak sebagai kredensial tidak valid, dapat: %v", id, err)
		}
	}
}
