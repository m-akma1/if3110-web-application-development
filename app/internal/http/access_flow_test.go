package http_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dbplatform/app/internal/accesstest"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/groups"
)

// Signup with and without an invite code, then what each user can see.
func TestSignupJoinsGroups_AndNavbarShowsAccess_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()

	target := accesstest.InsertTarget(t, env.db, "PostgreSQL Demo")
	eng, err := env.groups.Create(ctx, "Engineers", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.groups.CreateInviteCode(ctx, "ENG-CODE", eng.ID, ""); err != nil {
		t.Fatal(err)
	}
	accesstest.Grant(t, env.db, eng.ID, target, "VIEW_EDIT")

	// 1. Without a code: only Public, sees the empty state, no Migration menu.
	alice := newClient(t, env.srv)
	rec := alice.post("/auth/signup", url.Values{"username": {"alice"}, "password": {"PasswordAlice1"}})
	if rec.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("signup tanpa kode gagal: %s", rec.Body.String())
	}
	body := alice.get("/dashboard").Body.String()
	if !strings.Contains(body, "Belum ada database yang dapat diakses") || strings.Contains(body, `href="/migration"`) {
		t.Fatalf("user tanpa grant seharusnya melihat empty state tanpa menu Migration")
	}
	aliceID := userID(t, env.srv, "alice")
	mine, _ := env.groups.ListForUser(ctx, aliceID)
	if len(mine) != 1 || mine[0].ID != groups.PublicGroupID {
		t.Fatalf("alice seharusnya hanya anggota Public, dapat %+v", mine)
	}

	// 2. With a code (any case): joins Engineers and sees the database with View + Edit.
	bob := newClient(t, env.srv)
	rec = bob.post("/auth/signup", url.Values{"username": {"bob"}, "password": {"PasswordBob123"}, "invite_code": {"eng-code"}})
	if rec.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("signup dengan kode gagal: %s", rec.Body.String())
	}
	body = bob.get("/dashboard").Body.String()
	if !strings.Contains(body, "PostgreSQL Demo - View + Edit") || !strings.Contains(body, `href="/migration"`) {
		t.Fatalf("bob seharusnya melihat database dengan akses View + Edit dan menu Migration")
	}
	mig := bob.get("/migration").Body.String()
	if !strings.Contains(mig, `name="db_id"`) || !strings.Contains(mig, "Simpan Draft") {
		t.Fatalf("editor seharusnya melihat form draft migrasi dengan db_id")
	}

	// 3. Grant downgraded to VIEW_ONLY: effective immediately, the editor disappears.
	accesstest.Grant(t, env.db, eng.ID, target, "VIEW_ONLY")
	mig = bob.get("/migration").Body.String()
	if strings.Contains(mig, "Simpan Draft") || !strings.Contains(mig, "View Only") {
		t.Fatalf("setelah grant diturunkan, editor migrasi harus hilang")
	}
	if code := bob.post("/migration/drafts", url.Values{"db_id": {target}, "name": {"x"}, "sql_script": {"SELECT 1"}}).Code; code != http.StatusForbidden {
		t.Fatalf("membuat draft setelah grant diturunkan: status %d, ingin 403", code)
	}

	// 4. /api/auth/me reports groups and effective access.
	me := bob.get("/api/auth/me").Body.String()
	if !strings.Contains(me, `"name":"Engineers"`) || !strings.Contains(me, `"effective_access":"VIEW_ONLY"`) {
		t.Fatalf("/api/auth/me seharusnya memuat group dan akses efektif, dapat: %s", me)
	}
}

func TestReadOnlyGuard_StillApplies_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "demo")
	accesstest.Grant(t, env.db, groups.PublicGroupID, target, "VIEW_ONLY") // Public grant: everyone can read

	c := loggedIn(t, env.srv, "caca")
	rec := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"DELETE FROM customers"}})
	if !strings.Contains(rec.Body.String(), "DELETE") || !strings.Contains(rec.Body.String(), "ditolak") {
		t.Fatalf("DELETE seharusnya ditolak Read-Only Guard, dapat: %s", rec.Body.String())
	}
	_ = ctx
}

func TestAdminPage_ListsGroupsAndChangesRole_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()
	g, _ := env.groups.Create(ctx, "Analysts", "lihat saja")
	if _, err := env.groups.CreateInviteCode(ctx, "ANALYST-2026", g.ID, ""); err != nil {
		t.Fatal(err)
	}

	admin := loggedIn(t, env.srv, "moreno")
	page := admin.get("/admin")
	if page.Code != http.StatusOK {
		t.Fatalf("admin seharusnya bisa membuka /admin, status %d", page.Code)
	}
	for _, want := range []string{"Analysts", "ANALYST-2026", "Public", "caca"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("halaman admin seharusnya memuat %q", want)
		}
	}

	dhiaID := userID(t, env.srv, "dhia")
	rec := admin.post("/admin/users/"+dhiaID+"/role", url.Values{"role": {authn.RolePlatformAdmin}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Role diperbarui") {
		t.Fatalf("ubah role gagal: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := env.sessions.GetUserByUsername(ctx, "dhia"); u.Role != authn.RolePlatformAdmin {
		t.Fatalf("role dhia seharusnya PLATFORM_ADMIN, dapat %s", u.Role)
	}
	rec = admin.post("/admin/users/"+dhiaID+"/role", url.Values{"role": {"ROOT"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("role tidak valid seharusnya 422, dapat %d", rec.Code)
	}
}
