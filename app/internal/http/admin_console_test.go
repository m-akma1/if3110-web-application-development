package http_test

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/testutil"
)

func TestAdminRoutes_RequirePlatformAdmin_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	someID := ids.New()
	routes := []struct{ method, path string }{
		{http.MethodGet, "/admin"},
		{http.MethodPost, "/admin/users"},
		{http.MethodPost, "/admin/users/" + someID + "/role"},
		{http.MethodPost, "/admin/users/" + someID + "/active"},
		{http.MethodPost, "/admin/users/" + someID + "/password"},
		{http.MethodPost, "/admin/groups"},
		{http.MethodPost, "/admin/groups/" + someID},
		{http.MethodDelete, "/admin/groups/" + someID},
		{http.MethodPost, "/admin/groups/" + someID + "/members"},
		{http.MethodDelete, "/admin/groups/" + someID + "/members/" + someID},
		{http.MethodPost, "/admin/grants"},
		{http.MethodPost, "/admin/databases"},
		{http.MethodPost, "/admin/databases/" + someID},
		{http.MethodPost, "/admin/databases/" + someID + "/test"},
		{http.MethodDelete, "/admin/databases/" + someID},
		{http.MethodPost, "/admin/codes"},
		{http.MethodPost, "/admin/codes/SOME-CODE/active"},
	}
	user := loggedIn(t, env.srv, "dhia")
	anon := newClient(t, env.srv)
	for _, rt := range routes {
		req := func(c *client) int {
			r := newRequest(rt.method, rt.path, url.Values{"role": {"PLATFORM_ADMIN"}, "name": {"x"}})
			return c.do(r).Code
		}
		if code := req(user); code != http.StatusForbidden {
			t.Errorf("%s %s oleh USER: status %d, ingin 403", rt.method, rt.path, code)
		}
		if code := req(anon); code != http.StatusUnauthorized && code != http.StatusSeeOther {
			t.Errorf("%s %s tanpa sesi: status %d, ingin 401/303", rt.method, rt.path, code)
		}
	}
}

func newRequest(method, path string, form url.Values) *http.Request {
	r, _ := http.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.RequestURI = path
	return r
}

// Full flow through the admin console: register a real database, create a group, add a member,
// grant access, and the member can browse the live tables.
func TestAdminConsole_EndToEnd_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()

	// A table in the target database (the test DB itself), visible to ListTables (schema public).
	base, err := sql.Open("pgx", testutil.TestDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	table := "p4_e2e_" + strings.ReplaceAll(ids.New()[:8], "-", "")
	if _, err := base.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (id INT PRIMARY KEY, label TEXT)`, table)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = base.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })

	in, _ := dbmanager.TargetInputFromURL("E2E Target", testutil.TestDatabaseURL(t))
	admin := loggedIn(t, env.srv, "moreno")

	rec := admin.post("/admin/databases", url.Values{
		"name": {in.Name}, "host": {in.Host}, "port": {strconv.Itoa(in.Port)}, "db_name": {in.DBName},
		"username": {in.Username}, "password": {in.Password}, "ssl_mode": {"disable"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "berhasil terhubung") {
		t.Fatalf("mendaftarkan database gagal: %d %s", rec.Code, rec.Body.String())
	}
	targets, _ := env.targets.List(ctx)
	if len(targets) != 1 {
		t.Fatalf("seharusnya ada 1 target, dapat %d", len(targets))
	}
	targetID := targets[0].ID

	rec = admin.post("/admin/groups", url.Values{"name": {"Readers"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("membuat group gagal: %d %s", rec.Code, rec.Body.String())
	}
	readers, _ := env.groups.GetByName(ctx, "Readers")
	if rec := admin.post("/admin/groups/"+readers.ID+"/members", url.Values{"user_id": {userID(t, env.srv, "dhia")}}); rec.Code != http.StatusOK {
		t.Fatalf("menambah anggota gagal: %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.post("/admin/grants", url.Values{"group_id": {readers.ID}, "database_id": {targetID}, "level": {"VIEW_ONLY"}}); rec.Code != http.StatusOK {
		t.Fatalf("memberi akses gagal: %d %s", rec.Code, rec.Body.String())
	}

	dhia := loggedIn(t, env.srv, "dhia")
	page := dhia.get("/dashboard?db_id=" + targetID)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "E2E Target - View Only") || !strings.Contains(page.Body.String(), table) {
		t.Fatalf("anggota seharusnya melihat database dan tabel live-nya: %d", page.Code)
	}
	if code := dhia.post("/migration/apply", url.Values{"db_id": {targetID}}).Code; code != http.StatusForbidden {
		t.Fatalf("VIEW_ONLY tidak boleh menjalankan migrasi, status %d", code)
	}

	// Deleting the database removes access right away.
	req := newRequest(http.MethodDelete, "/admin/databases/"+targetID, nil)
	if rec := admin.do(req); rec.Code != http.StatusOK {
		t.Fatalf("hapus database gagal: %d %s", rec.Code, rec.Body.String())
	}
	if code := dhia.get("/dashboard?db_id=" + targetID).Code; code != http.StatusForbidden {
		t.Fatalf("setelah database dihapus, akses harus ditolak, status %d", code)
	}
}

func TestAdminConsole_ValidationAndSafety_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	admin := loggedIn(t, env.srv, "moreno")
	morenoID := userID(t, env.srv, "moreno")

	cases := []struct {
		name string
		path string
		form url.Values
		want string
	}{
		{"port tidak valid", "/admin/databases", url.Values{"name": {"X DB"}, "host": {"h"}, "port": {"99999"}, "db_name": {"d"}, "username": {"u"}, "password": {"p"}}, "port harus 1-65535"},
		{"turunkan role sendiri", "/admin/users/" + morenoID + "/role", url.Values{"role": {"USER"}}, "akun anda sendiri"},
		{"nonaktifkan diri sendiri", "/admin/users/" + morenoID + "/active", url.Values{"active": {"false"}}, "akun anda sendiri"},
		{"username dipakai", "/admin/users", url.Values{"username": {"dhia"}, "password": {"PasswordValid1"}}, "username sudah terdaftar"},
		{"password lemah", "/admin/users/" + userID(t, env.srv, "dhia") + "/password", url.Values{"password": {"pendek"}}, "password minimal 8"},
		{"kode ke Public", "/admin/codes", url.Values{"group_id": {groups.PublicGroupID}, "code": {"PUBLIC-1"}}, "group sistem"},
		{"level tidak valid", "/admin/grants", url.Values{"group_id": {groups.PublicGroupID}, "database_id": {ids.New()}, "level": {"ROOT"}}, "tingkat akses tidak valid"},
	}
	for _, tc := range cases {
		rec := admin.post(tc.path, tc.form)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: ingin 422 berisi %q, dapat %d", tc.name, tc.want, rec.Code)
		}
	}

	// Self-lockout UI: the admin's own row has no role selector or deactivate button.
	page := html.UnescapeString(admin.get("/admin?tab=databases").Body.String())
	if !strings.Contains(page, "activeTab: 'databases'") {
		t.Errorf("parameter ?tab seharusnya memilih tab awal")
	}
	if strings.Contains(page, "/admin/users/"+morenoID+"/role") || strings.Contains(page, "/admin/users/"+morenoID+"/active") {
		t.Errorf("baris admin sendiri tidak boleh menampilkan kontrol role/nonaktif")
	}
}
