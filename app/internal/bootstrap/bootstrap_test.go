package bootstrap_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/bootstrap"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/internal/testutil"
)

// Demo fixtures: alice is an admin, bob an engineer, carol an analyst without an email.
var (
	demoUsers = []bootstrap.DemoUser{
		{Username: "alice", Email: "alice@example.com", Password: "DemoPassword123", Role: authn.RolePlatformAdmin},
		{Username: "bob", Email: "bob@example.com", Password: "DemoPassword123", Role: authn.RoleUser},
		{Username: "carol", Password: "DemoPassword123"}, // no email, empty role: USER
	}
	demoGroups = []bootstrap.DemoGroup{
		{Name: "Engineers", Description: "Demo engineers", InviteCode: "ENGINEERS-TEST", Grant: "VIEW_EDIT", Members: []string{"bob"}},
		{Name: "Analysts", Description: "Demo analysts", InviteCode: "ANALYSTS-TEST", Grant: "VIEW_ONLY", Members: []string{"carol"}},
	}
)

func TestEnsureAdmin_CreatesFirstAdminOnce(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	admins := []bootstrap.Admin{
		{Username: "root.admin", Email: "root@example.com", Password: "RootPassword123"},
		{Username: "ops.admin", Password: "OpsPassword123"},
	}

	created, err := bootstrap.EnsureAdmin(ctx, store, admins)
	if err != nil || len(created) != 2 {
		t.Fatalf("semua admin awal seharusnya dibuat: created=%v err=%v", created, err)
	}
	for _, name := range []string{"root.admin", "ops.admin"} {
		u, err := store.GetUserByUsername(ctx, name)
		if err != nil || u.Role != authn.RolePlatformAdmin {
			t.Fatalf("%s seharusnya PLATFORM_ADMIN: %+v, %v", name, u, err)
		}
	}

	// Boot kedua: sudah ada admin aktif -> tidak melakukan apa pun, walau password env berubah
	admins[0].Password = "PasswordBaruDariEnv1"
	admins = append(admins, bootstrap.Admin{Username: "late.admin", Password: "LatePassword123"})
	created, err = bootstrap.EnsureAdmin(ctx, store, admins)
	if err != nil || len(created) != 0 {
		t.Fatalf("boot kedua tidak boleh membuat/mengubah admin: created=%v err=%v", created, err)
	}
	if _, _, err := store.Login(ctx, "root.admin", "RootPassword123", ""); err != nil {
		t.Fatalf("password admin tidak boleh ditimpa saat restart: %v", err)
	}
}

func TestEnsureAdmin_FailsClosedWithoutConfig(t *testing.T) {
	store := authn.NewMemorySessionStore()
	if _, err := bootstrap.EnsureAdmin(context.Background(), store, nil); !errors.Is(err, bootstrap.ErrNoAdminConfigured) {
		t.Fatalf("tanpa admin dan tanpa BOOTSTRAP_ADMINS seharusnya gagal, dapat: %v", err)
	}
}

func TestEnsureAdmin_DoesNotPromoteExistingUser(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	if _, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "admin", Password: "PasswordUserBiasa1"}); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrap.EnsureAdmin(ctx, store, []bootstrap.Admin{{Username: "admin", Password: "PasswordAdmin123"}})
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

	created, err := bootstrap.SeedDemo(ctx, store, nil, demoUsers, nil)
	if err != nil || len(created) != len(demoUsers) {
		t.Fatalf("seed pertama seharusnya membuat semua akun demo: %v, %v", created, err)
	}
	if u, _ := store.GetUserByUsername(ctx, "alice"); u == nil || u.Role != authn.RolePlatformAdmin {
		t.Fatalf("role dari SEED_DEMO_USERS seharusnya dipakai: %+v", u)
	}
	if u, _ := store.GetUserByUsername(ctx, "carol"); u == nil || u.Role != authn.RoleUser {
		t.Fatalf("role kosong seharusnya menjadi USER: %+v", u)
	}

	// Seed ulang dengan password berbeda tidak boleh menimpa apa pun
	changed := append([]bootstrap.DemoUser(nil), demoUsers...)
	for i := range changed {
		changed[i].Password = "PasswordLainLagi9"
	}
	created, err = bootstrap.SeedDemo(ctx, store, nil, changed, nil)
	if err != nil || len(created) != 0 {
		t.Fatalf("seed kedua tidak boleh membuat/mengubah akun: %v, %v", created, err)
	}
	if _, _, err := store.Login(ctx, "bob", "DemoPassword123", ""); err != nil {
		t.Fatalf("password demo tidak boleh berubah setelah seed ulang: %v", err)
	}
	if _, _, err := store.Login(ctx, "carol", "DemoPassword123", ""); err != nil {
		t.Fatalf("akun demo tanpa email seharusnya bisa login via username: %v", err)
	}
}

func TestSeedDemo_PerUserPasswords(t *testing.T) {
	ctx := context.Background()
	store := authn.NewMemorySessionStore()
	users := []bootstrap.DemoUser{
		{Username: "alice", Password: "AlicePassword1"},
		{Username: "bob", Password: "BobPassword1"},
	}
	if _, err := bootstrap.SeedDemo(ctx, store, nil, users, nil); err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if _, _, err := store.Login(ctx, u.Username, u.Password, ""); err != nil {
			t.Fatalf("%s seharusnya login dengan password miliknya: %v", u.Username, err)
		}
	}
	if _, _, err := store.Login(ctx, "alice", "BobPassword1", ""); err == nil {
		t.Fatal("password bob tidak boleh berlaku untuk alice")
	}
}

func TestSeedDemo_RejectsInvalidUser(t *testing.T) {
	users := []bootstrap.DemoUser{{Username: "alice", Password: "short"}}
	if _, err := bootstrap.SeedDemo(context.Background(), authn.NewMemorySessionStore(), nil, users, nil); err == nil {
		t.Fatalf("akun demo dengan password lemah seharusnya gagal")
	}
}

func TestSeedDemo_Groups_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	repo := groups.NewRepository(db)

	if _, err := bootstrap.SeedDemo(ctx, store, repo, demoUsers, demoGroups); err != nil {
		t.Fatalf("seed gagal: %v", err)
	}
	eng, err := repo.ResolveInviteCode(ctx, "ENGINEERS-TEST")
	if err != nil || eng.Name != "Engineers" || eng.MemberCount != 1 {
		t.Fatalf("group Engineers + kode undangan seharusnya dibuat dengan 1 anggota: %+v, %v", eng, err)
	}

	// Admin mengeluarkan bob dari Engineers; restart (seed ulang) tidak boleh memasukkannya kembali.
	bob, _ := store.GetUserByUsername(ctx, "bob")
	if _, err := db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, eng.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	created, err := bootstrap.SeedDemo(ctx, store, repo, demoUsers, demoGroups)
	if err != nil || len(created) != 0 {
		t.Fatalf("seed ulang tidak boleh membuat apa pun: %v, %v", created, err)
	}
	eng, _ = repo.Get(ctx, eng.ID)
	if eng.MemberCount != 0 {
		t.Fatalf("seed ulang tidak boleh mengembalikan keanggotaan yang dihapus admin, anggota: %d", eng.MemberCount)
	}
}

func TestSeedDemoTarget_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	repo := groups.NewRepository(db)
	grants := rbac.NewPostgresGrantStore(db)
	cipher, _ := dbmanager.NewCredentialCipher(testutil.CredentialsKey(t))
	reg := dbmanager.NewConnectionRegistry()
	t.Cleanup(reg.Close)
	targets := dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, reg)

	if _, err := bootstrap.SeedDemo(ctx, store, repo, demoUsers, demoGroups); err != nil {
		t.Fatal(err)
	}
	demoTarget, err := dbmanager.TargetInputFromURL("ignored", testutil.TestDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	created, err := bootstrap.SeedDemoTarget(ctx, targets, grants, repo, demoTarget, demoGroups)
	if err != nil || !created {
		t.Fatalf("database demo seharusnya didaftarkan: %v, %v", created, err)
	}
	list, _ := targets.List(ctx)
	if len(list) != 1 || list[0].Name != bootstrap.DemoTargetName || list[0].Status != dbmanager.StatusConnected {
		t.Fatalf("target demo seharusnya terdaftar dan terhubung: %+v", list)
	}

	authz := rbac.NewAuthorizer(grants)
	bob, _ := store.GetUserByUsername(ctx, "bob")
	carol, _ := store.GetUserByUsername(ctx, "carol")
	if lvl, _ := authz.EffectiveLevel(ctx, bob.ID, list[0].ID); lvl != rbac.AccessViewEdit {
		t.Fatalf("bob (Engineers) seharusnya VIEW_EDIT, dapat %s", lvl)
	}
	if lvl, _ := authz.EffectiveLevel(ctx, carol.ID, list[0].ID); lvl != rbac.AccessViewOnly {
		t.Fatalf("carol (Analysts) seharusnya VIEW_ONLY, dapat %s", lvl)
	}

	// Admin removes the Engineers grant; restarting must not bring it back.
	eng, _ := repo.GetByName(ctx, "Engineers")
	if _, err := grants.SetGroupGrant(ctx, eng.ID, list[0].ID, rbac.AccessNone); err != nil {
		t.Fatal(err)
	}
	created, err = bootstrap.SeedDemoTarget(ctx, targets, grants, repo, demoTarget, demoGroups)
	if err != nil || created {
		t.Fatalf("seed ulang tidak boleh membuat apa pun: %v, %v", created, err)
	}
	if lvl, _ := authz.EffectiveLevel(ctx, bob.ID, list[0].ID); lvl != rbac.AccessNone {
		t.Fatalf("grant yang dihapus admin tidak boleh kembali, dapat %s", lvl)
	}
}
