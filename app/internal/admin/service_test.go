package admin_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/app/internal/accesstest"
	"dbplatform/app/internal/admin"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/internal/testutil"
)

type env struct {
	svc      *admin.Service
	sessions *authn.SessionStore
	groups   *groups.Repository
	authz    *rbac.Authorizer
	adminID  string
}

func newEnv(t *testing.T) (*env, func(string) string) {
	t.Helper()
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	sessions := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	gr := groups.NewRepository(db)
	grants := rbac.NewPostgresGrantStore(db)
	cipher, _ := dbmanager.NewCredentialCipher(testutil.CredentialsKey(t))
	reg := dbmanager.NewConnectionRegistry()
	t.Cleanup(reg.Close)
	svc := admin.NewService(sessions, gr, grants, dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, reg))

	boss, err := sessions.RegisterUser(ctx, authn.RegisterInput{Username: "boss", Password: "PasswordBoss12", Role: authn.RolePlatformAdmin})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: svc, sessions: sessions, groups: gr, authz: rbac.NewAuthorizer(grants), adminID: boss.ID}
	insertTarget := func(name string) string { return accesstest.InsertTarget(t, db, name) }
	return e, insertTarget
}

// login returns a session token for a fresh user.
func (e *env) newUser(t *testing.T, name string) (string, string) {
	t.Helper()
	ctx := context.Background()
	u, err := e.sessions.RegisterUser(ctx, authn.RegisterInput{Username: name, Password: "Password-" + name})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := e.sessions.Login(ctx, name, "Password-"+name, "")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID, tok
}

// rotatesNext reports whether the session rotates on its next request (and is still valid).
func (e *env) rotatesNext(t *testing.T, tok string) bool {
	t.Helper()
	res, err := e.sessions.ValidateAndRefresh(context.Background(), tok)
	if err != nil {
		t.Fatalf("sesi seharusnya masih valid: %v", err)
	}
	return res.Rotated
}

func (e *env) revoked(tok string) bool {
	_, err := e.sessions.ValidateAndRefresh(context.Background(), tok)
	return err != nil
}

func TestRoleChanges_RotateOrRevokeSessions_Integration(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	uid, tok := e.newUser(t, "calon")

	if err := e.svc.SetUserRole(ctx, e.adminID, uid, authn.RolePlatformAdmin); err != nil {
		t.Fatal(err)
	}
	if !e.rotatesNext(t, tok) {
		t.Fatalf("promosi ke admin harus merotasi sesi user pada request berikutnya")
	}

	tok2, _, _ := e.sessions.Login(ctx, "calon", "Password-calon", "")
	if err := e.svc.SetUserRole(ctx, e.adminID, uid, authn.RoleUser); err != nil {
		t.Fatal(err)
	}
	if !e.revoked(tok2) {
		t.Fatalf("penurunan role harus mencabut sesi user")
	}
	if err := e.svc.SetUserRole(ctx, e.adminID, uid, "ROOT"); !errors.Is(err, authn.ErrInvalidRole) {
		t.Fatalf("role tidak valid seharusnya ditolak, dapat %v", err)
	}
}

func TestSelfLockoutIsBlocked_Integration(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	if err := e.svc.SetUserRole(ctx, e.adminID, e.adminID, authn.RoleUser); !errors.Is(err, admin.ErrSelfLockout) {
		t.Fatalf("admin tidak boleh menurunkan role sendiri, dapat %v", err)
	}
	if err := e.svc.SetUserActive(ctx, e.adminID, e.adminID, false); !errors.Is(err, admin.ErrSelfLockout) {
		t.Fatalf("admin tidak boleh menonaktifkan diri sendiri, dapat %v", err)
	}
}

func TestDeactivateReactivateAndResetPassword_Integration(t *testing.T) {
	e, _ := newEnv(t)
	ctx := context.Background()
	uid, tok := e.newUser(t, "pegawai")

	if err := e.svc.SetUserActive(ctx, e.adminID, uid, false); err != nil {
		t.Fatal(err)
	}
	if !e.revoked(tok) {
		t.Fatalf("menonaktifkan akun harus mencabut sesinya")
	}
	if _, _, err := e.sessions.Login(ctx, "pegawai", "Password-pegawai", ""); err == nil {
		t.Fatalf("akun nonaktif tidak boleh login")
	}
	if err := e.svc.SetUserActive(ctx, e.adminID, uid, true); err != nil {
		t.Fatal(err)
	}
	tok, _, err := e.sessions.Login(ctx, "pegawai", "Password-pegawai", "")
	if err != nil {
		t.Fatalf("akun aktif kembali seharusnya bisa login: %v", err)
	}
	if err := e.svc.ResetPassword(ctx, uid, "PasswordBaru123"); err != nil {
		t.Fatal(err)
	}
	if !e.revoked(tok) {
		t.Fatalf("reset password harus mencabut sesi")
	}
	if err := e.svc.ResetPassword(ctx, "00000000-0000-0000-0000-00000000dead", "PasswordBaru123"); !errors.Is(err, authn.ErrUserNotFound) {
		t.Fatalf("user tidak dikenal seharusnya ErrUserNotFound, dapat %v", err)
	}
}

func TestGroupMembershipAndGrants_Integration(t *testing.T) {
	e, insertTarget := newEnv(t)
	ctx := context.Background()
	db := insertTarget("prod")
	uid, tok := e.newUser(t, "dev")

	g, err := e.svc.CreateGroup(ctx, "Developers", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetGrant(ctx, g.ID, db, "VIEW_ONLY"); err != nil {
		t.Fatal(err)
	}

	// Joining a group can only add access: rotate.
	if err := e.svc.AddMember(ctx, g.ID, uid); err != nil {
		t.Fatal(err)
	}
	if !e.rotatesNext(t, tok) {
		t.Fatalf("masuk group harus merotasi sesi")
	}
	if lvl, _ := e.authz.EffectiveLevel(ctx, uid, db); lvl != rbac.AccessViewOnly {
		t.Fatalf("akses efektif seharusnya VIEW_ONLY, dapat %s", lvl)
	}

	// Raising a grant rotates every member; lowering it does not, but applies immediately.
	tok, _, _ = e.sessions.Login(ctx, "dev", "Password-dev", "")
	if err := e.svc.SetGrant(ctx, g.ID, db, "VIEW_EDIT"); err != nil {
		t.Fatal(err)
	}
	if !e.rotatesNext(t, tok) {
		t.Fatalf("grant dinaikkan harus merotasi sesi anggota")
	}
	tok, _, _ = e.sessions.Login(ctx, "dev", "Password-dev", "")
	if err := e.svc.SetGrant(ctx, g.ID, db, "VIEW_ONLY"); err != nil {
		t.Fatal(err)
	}
	if e.rotatesNext(t, tok) {
		t.Fatalf("grant diturunkan tidak perlu merotasi sesi")
	}
	if lvl, _ := e.authz.EffectiveLevel(ctx, uid, db); lvl != rbac.AccessViewOnly {
		t.Fatalf("penurunan grant harus langsung berlaku, dapat %s", lvl)
	}

	if err := e.svc.SetGrant(ctx, g.ID, db, "SUPERUSER"); !errors.Is(err, admin.ErrInvalidLevel) {
		t.Fatalf("level tidak valid seharusnya ditolak, dapat %v", err)
	}
	if err := e.svc.RemoveMember(ctx, groups.PublicGroupID, uid); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("keluar dari Public seharusnya ditolak, dapat %v", err)
	}
	if err := e.svc.RemoveMember(ctx, g.ID, uid); err != nil {
		t.Fatal(err)
	}
	if lvl, _ := e.authz.EffectiveLevel(ctx, uid, db); lvl != rbac.AccessNone {
		t.Fatalf("keluar dari group harus langsung mencabut akses, dapat %s", lvl)
	}
	if err := e.svc.DeleteGroup(ctx, groups.PublicGroupID); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("Public tidak boleh dihapus, dapat %v", err)
	}
}

func TestCreateUserWithGroupsAndOverview_Integration(t *testing.T) {
	e, insertTarget := newEnv(t)
	ctx := context.Background()
	db := insertTarget("analytics")
	g, _ := e.svc.CreateGroup(ctx, "Analysts", "lihat saja")
	_ = e.svc.SetGrant(ctx, g.ID, db, "VIEW_ONLY")
	if _, err := e.svc.CreateInviteCode(ctx, e.adminID, g.ID, "ANALYST-1"); err != nil {
		t.Fatal(err)
	}

	u, err := e.svc.CreateUser(ctx, authn.RegisterInput{Username: "sinta", Password: "PasswordSinta1"}, []string{g.ID, groups.PublicGroupID})
	if err != nil {
		t.Fatal(err)
	}
	if lvl, _ := e.authz.EffectiveLevel(ctx, u.ID, db); lvl != rbac.AccessViewOnly {
		t.Fatalf("user baru seharusnya mendapat akses lewat group, dapat %s", lvl)
	}

	ov, err := e.svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Users) != 2 || len(ov.Targets) != 1 || len(ov.Codes) != 1 || len(ov.Groups) != 2 {
		t.Fatalf("overview tidak lengkap: users=%d targets=%d codes=%d groups=%d", len(ov.Users), len(ov.Targets), len(ov.Codes), len(ov.Groups))
	}
	var analysts bool
	for _, gi := range ov.Groups {
		if gi.ID == g.ID {
			analysts = gi.GrantFor(db) == "VIEW_ONLY" && gi.HasMember(u.ID) && gi.MemberCount == 1
		}
	}
	if !analysts {
		t.Fatalf("group Analysts di overview seharusnya punya grant dan anggota: %+v", ov.Groups)
	}
}
