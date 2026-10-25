package authn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbplatform/internal/authn"
)

func newClockStore(t *testing.T) (*authn.SessionStore, *time.Time) {
	t.Helper()
	store := authn.NewSessionStoreWithRepo(authn.NewMemoryRepository(), authn.SessionConfig{
		IdleTimeout: 30 * time.Minute, AbsoluteExpiration: 12 * time.Hour,
		RotationInterval: 15 * time.Minute, GracePeriod: 15 * time.Second,
	})
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	return store, &now
}

func TestForceRotateUserSessions_RotatesOnNextRequest(t *testing.T) {
	ctx := context.Background()
	store, now := newClockStore(t)
	u, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "naik", Password: "PasswordNaik1"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := store.Login(ctx, "naik", "PasswordNaik1", "")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.ValidateAndRefresh(ctx, tok)
	absolute := first.Session.AbsoluteExpiresAt

	// One minute later, well inside the 15-minute rotation interval, the admin raises privilege.
	*now = now.Add(time.Minute)
	if err := store.ForceRotateUserSessions(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	res, err := store.ValidateAndRefresh(ctx, tok)
	if err != nil || !res.Rotated || res.NewOpaqueID == "" || res.NewOpaqueID == tok {
		t.Fatalf("sesi seharusnya dirotasi pada request berikutnya setelah privilege naik: %+v, %v", res, err)
	}
	if !res.Session.AbsoluteExpiresAt.Equal(absolute) {
		t.Fatalf("rotasi karena privilege tidak boleh memperpanjang absolute expiration")
	}
	// Old identifier still works inside the grace period, then stops.
	if _, err := store.ValidateAndRefresh(ctx, tok); err != nil {
		t.Fatalf("token lama seharusnya masih berlaku selama grace period: %v", err)
	}
	*now = now.Add(20 * time.Second)
	if _, err := store.ValidateAndRefresh(ctx, tok); err == nil {
		t.Fatalf("token lama seharusnya ditolak setelah grace period")
	}
}

func TestRevokeAllSessions_KeepsAccountUsable(t *testing.T) {
	ctx := context.Background()
	store, _ := newClockStore(t)
	u, _ := store.RegisterUser(ctx, authn.RegisterInput{Username: "turun", Password: "PasswordTurun1"})
	tok, _, _ := store.Login(ctx, "turun", "PasswordTurun1", "")

	if err := store.RevokeAllSessions(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAndRefresh(ctx, tok); err == nil {
		t.Fatalf("sesi seharusnya dicabut")
	}
	if _, _, err := store.Login(ctx, "turun", "PasswordTurun1", ""); err != nil {
		t.Fatalf("akun tetap aktif, user harus bisa login ulang: %v", err)
	}
}

func TestSetPassword_RevokesSessionsAndChangesCredential(t *testing.T) {
	ctx := context.Background()
	store, _ := newClockStore(t)
	u, _ := store.RegisterUser(ctx, authn.RegisterInput{Username: "reset", Password: "PasswordLama1"})
	tok, _, _ := store.Login(ctx, "reset", "PasswordLama1", "")

	if err := store.SetPassword(ctx, u.ID, "pendek"); !errors.Is(err, authn.ErrWeakPassword) {
		t.Fatalf("password lemah seharusnya ditolak, dapat %v", err)
	}
	if err := store.SetPassword(ctx, u.ID, "PasswordBaru1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAndRefresh(ctx, tok); err == nil {
		t.Fatalf("reset password harus mencabut sesi lama")
	}
	if _, _, err := store.Login(ctx, "reset", "PasswordLama1", ""); err == nil {
		t.Fatalf("password lama tidak boleh berlaku lagi")
	}
	if _, _, err := store.Login(ctx, "reset", "PasswordBaru1", ""); err != nil {
		t.Fatalf("password baru seharusnya berlaku: %v", err)
	}
}

func TestReactivateUser(t *testing.T) {
	ctx := context.Background()
	store, _ := newClockStore(t)
	u, _ := store.RegisterUser(ctx, authn.RegisterInput{Username: "cuti", Password: "PasswordCuti1"})
	if err := store.RevokeUserAccess(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Login(ctx, "cuti", "PasswordCuti1", ""); err == nil {
		t.Fatalf("akun nonaktif tidak boleh login")
	}
	if err := store.ReactivateUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Login(ctx, "cuti", "PasswordCuti1", ""); err != nil {
		t.Fatalf("akun yang diaktifkan kembali seharusnya bisa login: %v", err)
	}
}
