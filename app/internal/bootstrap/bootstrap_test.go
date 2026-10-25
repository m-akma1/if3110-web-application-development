package bootstrap_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/bootstrap"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/testutil"
)

func TestEnsureAdmin_CreatesFirstAdminOnce(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	cfg := bootstrap.AdminConfig{Username: "root.admin", Email: "root@example.com", Password: "RootPassword123"}

	created, err := bootstrap.EnsureAdmin(ctx, store, cfg)
	if err != nil || !created {
		t.Fatalf("admin awal seharusnya dibuat: created=%v err=%v", created, err)
	}
	u, err := store.GetUserByUsername(ctx, "root.admin")
	if err != nil || u.Role != authn.RolePlatformAdmin {
		t.Fatalf("admin awal seharusnya PLATFORM_ADMIN: %+v, %v", u, err)
	}

	// Boot kedua: sudah ada admin aktif -> tidak melakukan apa pun, walau password env berubah
	cfg.Password = "PasswordBaruDariEnv1"
	created, err = bootstrap.EnsureAdmin(ctx, store, cfg)
	if err != nil || created {
		t.Fatalf("boot kedua tidak boleh membuat/mengubah admin: created=%v err=%v", created, err)
	}
	if _, _, err := store.Login(ctx, "root.admin", "RootPassword123", ""); err != nil {
		t.Fatalf("password admin tidak boleh ditimpa saat restart: %v", err)
	}
}

func TestEnsureAdmin_FailsClosedWithoutConfig(t *testing.T) {
	store := authn.NewMemorySessionStore()
	if _, err := bootstrap.EnsureAdmin(context.Background(), store, bootstrap.AdminConfig{}); !errors.Is(err, bootstrap.ErrNoAdminConfigured) {
		t.Fatalf("tanpa admin dan tanpa BOOTSTRAP_ADMIN_* seharusnya gagal, dapat: %v", err)
	}
}

func TestEnsureAdmin_DoesNotPromoteExistingUser(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "admin", Password: "PasswordUserBiasa1"}); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrap.EnsureAdmin(ctx, store, bootstrap.AdminConfig{Username: "admin", Password: "PasswordAdmin123"})
	if !errors.Is(err, authn.ErrUsernameTaken) {
		t.Fatalf("username yang sudah dipakai user biasa tidak boleh dipromosikan diam-diam, dapat: %v", err)
	}
	u, _ := store.GetUserByUsername(ctx, "admin")
	if u.Role != authn.RoleUser {
		t.Fatalf("role user lama tidak boleh berubah, dapat: %s", u.Role)
	}
}

func TestSeedDemo_InsertOnly(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()

	created, err := bootstrap.SeedDemo(ctx, store, nil, bootstrap.DemoConfig{Password: "DemoPassword123"})
	if err != nil || len(created) != len(bootstrap.DemoUsers) {
		t.Fatalf("seed pertama seharusnya membuat semua akun demo: %v, %v", created, err)
	}

	// Seed ulang dengan password berbeda tidak boleh menimpa apa pun
	created, err = bootstrap.SeedDemo(ctx, store, nil, bootstrap.DemoConfig{Password: "PasswordLainLagi9"})
	if err != nil || len(created) != 0 {
		t.Fatalf("seed kedua tidak boleh membuat/mengubah akun: %v, %v", created, err)
	}
	if _, _, err := store.Login(ctx, "akmal", "DemoPassword123", ""); err != nil {
		t.Fatalf("password demo tidak boleh berubah setelah seed ulang: %v", err)
	}
	if _, _, err := store.Login(ctx, "dzaki", "DemoPassword123", ""); err != nil {
		t.Fatalf("akun demo tanpa email seharusnya bisa login via username: %v", err)
	}
}

func TestSeedDemo_RequiresPassword(t *testing.T) {
	if _, err := bootstrap.SeedDemo(context.Background(), authn.NewMemorySessionStore(), nil, bootstrap.DemoConfig{}); err == nil {
		t.Fatalf("seed tanpa password seharusnya gagal")
	}
}

func TestSeedDemo_Groups_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	repo := groups.NewRepository(db)

	if _, err := bootstrap.SeedDemo(ctx, store, repo, bootstrap.DemoConfig{Password: "DemoPassword123"}); err != nil {
		t.Fatalf("seed gagal: %v", err)
	}
	eng, err := repo.ResolveInviteCode(ctx, "ENGINEERS-DEMO")
	if err != nil || eng.Name != "Engineers" || eng.MemberCount != 1 {
		t.Fatalf("group Engineers + kode undangan seharusnya dibuat dengan 1 anggota: %+v, %v", eng, err)
	}

	// Admin mengeluarkan akmal dari Engineers; restart (seed ulang) tidak boleh memasukkannya kembali.
	akmal, _ := store.GetUserByUsername(ctx, "akmal")
	if _, err := db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, eng.ID, akmal.ID); err != nil {
		t.Fatal(err)
	}
	created, err := bootstrap.SeedDemo(ctx, store, repo, bootstrap.DemoConfig{Password: "DemoPassword123"})
	if err != nil || len(created) != 0 {
		t.Fatalf("seed ulang tidak boleh membuat apa pun: %v, %v", created, err)
	}
	eng, _ = repo.Get(ctx, eng.ID)
	if eng.MemberCount != 0 {
		t.Fatalf("seed ulang tidak boleh mengembalikan keanggotaan yang dihapus admin, anggota: %d", eng.MemberCount)
	}
}
