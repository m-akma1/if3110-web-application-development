package authn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/testutil"
)

// runRepositoryContract memastikan PostgresRepository dan MemoryRepository berperilaku sama.
func runRepositoryContract(t *testing.T, repo authn.Repository) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	alice := &authn.User{ID: ids.New(), Username: "alice", Email: "alice@example.com", PasswordHash: "h", Role: authn.RoleUser, IsActive: true, CreatedAt: now}
	bob := &authn.User{ID: ids.New(), Username: "bob", PasswordHash: "h", Role: authn.RolePlatformAdmin, IsActive: true, CreatedAt: now.Add(time.Second)}
	for _, u := range []*authn.User{alice, bob} {
		if err := repo.CreateUser(ctx, u); err != nil {
			t.Fatalf("CreateUser(%s): %v", u.Username, err)
		}
	}

	t.Run("duplikat tidak menimpa data", func(t *testing.T) {
		dupName := &authn.User{ID: ids.New(), Username: "alice", PasswordHash: "lain", Role: authn.RolePlatformAdmin, IsActive: true, CreatedAt: now}
		if err := repo.CreateUser(ctx, dupName); !errors.Is(err, authn.ErrUsernameTaken) {
			t.Fatalf("username duplikat seharusnya ErrUsernameTaken, dapat: %v", err)
		}
		dupEmail := &authn.User{ID: ids.New(), Username: "alice2", Email: "alice@example.com", PasswordHash: "lain", Role: authn.RoleUser, IsActive: true, CreatedAt: now}
		if err := repo.CreateUser(ctx, dupEmail); !errors.Is(err, authn.ErrEmailTaken) {
			t.Fatalf("email duplikat seharusnya ErrEmailTaken, dapat: %v", err)
		}
		got, err := repo.GetUserByUsername(ctx, "alice")
		if err != nil || got.PasswordHash != "h" || got.Role != authn.RoleUser {
			t.Fatalf("data alice tidak boleh tertimpa, dapat: %+v, err=%v", got, err)
		}
	})

	t.Run("lookup", func(t *testing.T) {
		if u, err := repo.GetUserByEmail(ctx, "ALICE@example.com"); err != nil || u.ID != alice.ID {
			t.Fatalf("GetUserByEmail case-insensitive gagal: %+v, %v", u, err)
		}
		if u, err := repo.GetUserByID(ctx, bob.ID); err != nil || u.Email != "" {
			t.Fatalf("bob tanpa email seharusnya email kosong: %+v, %v", u, err)
		}
		if _, err := repo.GetUserByEmail(ctx, ""); !errors.Is(err, authn.ErrUserNotFound) {
			t.Fatalf("email kosong tidak boleh cocok dengan user tanpa email, dapat: %v", err)
		}
		if _, err := repo.GetUserByID(ctx, "bukan-uuid"); !errors.Is(err, authn.ErrUserNotFound) {
			t.Fatalf("ID bukan UUID seharusnya ErrUserNotFound, dapat: %v", err)
		}
		list, err := repo.ListUsers(ctx)
		if err != nil || len(list) != 2 || list[0].Username != "alice" {
			t.Fatalf("ListUsers seharusnya [alice bob], dapat: %v, %v", list, err)
		}
	})

	t.Run("admin count, role, status", func(t *testing.T) {
		if n, err := repo.CountActiveAdmins(ctx); err != nil || n != 1 {
			t.Fatalf("seharusnya 1 admin aktif, dapat %d (%v)", n, err)
		}
		if err := repo.SetUserActiveStatus(ctx, bob.ID, false); err != nil {
			t.Fatal(err)
		}
		if n, _ := repo.CountActiveAdmins(ctx); n != 0 {
			t.Fatalf("admin nonaktif tidak boleh dihitung, dapat %d", n)
		}
		if err := repo.UpdateUserRole(ctx, alice.ID, "ROOT"); !errors.Is(err, authn.ErrInvalidRole) {
			t.Fatalf("role tidak valid seharusnya ditolak, dapat: %v", err)
		}
		if err := repo.UpdateUserRole(ctx, ids.New(), authn.RoleUser); !errors.Is(err, authn.ErrUserNotFound) {
			t.Fatalf("user tidak ada seharusnya ErrUserNotFound, dapat: %v", err)
		}
	})

	t.Run("rotasi sesi compare-and-swap", func(t *testing.T) {
		sess := &authn.Session{
			ID: ids.New(), UserID: alice.ID, TokenHash: authn.HashOpaqueToken("t1"),
			CreatedAt: now, LastActivityAt: now, LastRotatedAt: now,
			IdleExpiresAt: now.Add(30 * time.Minute), AbsoluteExpiresAt: now.Add(12 * time.Hour),
		}
		if err := repo.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		grace := now.Add(15 * time.Second)
		ok, err := repo.RotateSessionToken(ctx, sess.ID, authn.HashOpaqueToken("t1"), authn.HashOpaqueToken("t2"), grace, now, now.Add(30*time.Minute))
		if err != nil || !ok {
			t.Fatalf("rotasi pertama seharusnya berhasil: %v, %v", ok, err)
		}
		// Request paralel yang masih memegang t1 kalah dalam compare-and-swap
		ok, err = repo.RotateSessionToken(ctx, sess.ID, authn.HashOpaqueToken("t1"), authn.HashOpaqueToken("t3"), grace, now, now.Add(30*time.Minute))
		if err != nil || ok {
			t.Fatalf("rotasi kedua dengan hash lama seharusnya gagal tanpa error: %v, %v", ok, err)
		}
		got, err := repo.GetSessionByTokenHash(ctx, authn.HashOpaqueToken("t1"))
		if err != nil || got.TokenHash != authn.HashOpaqueToken("t2") || got.PreviousTokenHash != authn.HashOpaqueToken("t1") {
			t.Fatalf("sesi seharusnya ditemukan lewat hash lama dengan token aktif t2: %+v, %v", got, err)
		}
		if !got.AbsoluteExpiresAt.Equal(sess.AbsoluteExpiresAt) {
			t.Fatalf("rotasi tidak boleh mengubah absolute_expires_at")
		}
		if err := repo.MarkSessionsForRotation(ctx, alice.ID); err != nil {
			t.Fatal(err)
		}
		marked, _ := repo.GetSessionByTokenHash(ctx, authn.HashOpaqueToken("t2"))
		if marked == nil || marked.LastRotatedAt.Year() > 1970 {
			t.Fatalf("MarkSessionsForRotation seharusnya memundurkan last_rotated_at, dapat %+v", marked)
		}
		if err := repo.UpdatePasswordHash(ctx, alice.ID, "hash-baru"); err != nil {
			t.Fatal(err)
		}
		if u, _ := repo.GetUserByID(ctx, alice.ID); u.PasswordHash != "hash-baru" {
			t.Fatalf("password hash seharusnya diperbarui")
		}
		if err := repo.UpdatePasswordHash(ctx, ids.New(), "x"); !errors.Is(err, authn.ErrUserNotFound) {
			t.Fatalf("user tidak ada seharusnya ErrUserNotFound, dapat %v", err)
		}
		if err := repo.RevokeAllUserSessions(ctx, alice.ID); err != nil {
			t.Fatal(err)
		}
		got, _ = repo.GetSessionByTokenHash(ctx, authn.HashOpaqueToken("t2"))
		if got == nil || !got.IsRevoked {
			t.Fatalf("sesi seharusnya dicabut")
		}
	})
}

func TestRepositoryContract_Memory(t *testing.T) {
	runRepositoryContract(t, authn.NewMemoryRepository())
}

func TestRepositoryContract_Postgres_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	runRepositoryContract(t, authn.NewPostgresRepository(db))
}

// Alur penuh SessionStore di atas PostgreSQL: registrasi, login username/email, rotasi, logout
func TestSessionStore_Postgres_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.SessionConfig{
		IdleTimeout: 30 * time.Minute, AbsoluteExpiration: 12 * time.Hour,
		RotationInterval: 15 * time.Minute, GracePeriod: 15 * time.Second,
	})
	clock := time.Now()
	store.SetClock(func() time.Time { return clock })

	u, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "Rina", Email: "rina@example.com", Password: "PasswordRina123"})
	if err != nil {
		t.Fatalf("registrasi gagal: %v", err)
	}
	if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "rina", Password: "PasswordLain123"}); !errors.Is(err, authn.ErrUsernameTaken) {
		t.Fatalf("username duplikat (beda huruf) seharusnya ditolak, dapat: %v", err)
	}

	tok, profile, err := store.Login(ctx, "rina@example.com", "PasswordRina123", "")
	if err != nil || profile.ID != u.ID {
		t.Fatalf("login via email gagal: %v", err)
	}
	if _, _, err := store.Login(ctx, "RINA", "PasswordRina123", ""); err != nil {
		t.Fatalf("login via username gagal: %v", err)
	}

	clock = clock.Add(16 * time.Minute)
	res, err := store.ValidateAndRefresh(ctx, tok)
	if err != nil || !res.Rotated {
		t.Fatalf("sesi seharusnya dirotasi setelah 16 menit: %+v, %v", res, err)
	}
	if err := store.RevokeSession(ctx, res.NewOpaqueID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAndRefresh(ctx, tok); err == nil {
		t.Fatalf("token lama seharusnya ditolak setelah logout")
	}
}
