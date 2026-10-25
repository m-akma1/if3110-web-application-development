package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"dbplatform/app/internal/accesstest"
	"dbplatform/app/internal/authn"
)

// Every DB-scoped route × every kind of principal. The backend must enforce this,
// whatever the UI shows or hides.
func TestAuthorizationMatrix_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()

	target := accesstest.InsertTarget(t, env.db, "demo")
	viewers, err := env.groups.Create(ctx, "Viewers", "")
	if err != nil {
		t.Fatal(err)
	}
	editors, err := env.groups.Create(ctx, "Editors", "")
	if err != nil {
		t.Fatal(err)
	}
	accesstest.Grant(t, env.db, viewers.ID, target, "VIEW_ONLY")
	accesstest.Grant(t, env.db, editors.ID, target, "VIEW_EDIT")

	register := func(name string, role string, groupIDs ...string) {
		u, err := env.sessions.RegisterUser(ctx, authn.RegisterInput{Username: name, Password: "Password-" + name, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range groupIDs {
			if err := env.groups.AddMember(ctx, g, u.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	register("nogrant", authn.RoleUser)
	register("viewer", authn.RoleUser, viewers.ID)
	register("editor", authn.RoleUser, editors.ID)
	register("both", authn.RoleUser, viewers.ID, editors.ID) // most permissive wins
	register("adminnogrant", authn.RolePlatformAdmin)        // admin is not a DB access level

	type route struct {
		name   string
		method string
		path   string
		form   url.Values
	}
	routes := []route{
		{"viewer page", http.MethodGet, "/dashboard?db_id=" + target, nil},
		{"table fragment", http.MethodGet, "/dashboard/table?db_id=" + target + "&name=customers", nil},
		{"read-only query", http.MethodPost, "/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"SELECT 1"}}},
		{"migration page", http.MethodGet, "/migration?db_id=" + target, nil},
		{"migration dry-run", http.MethodPost, "/migration/dry-run", url.Values{"db_id": {target}, "sql_script": {"SELECT 1"}}},
		{"migration apply", http.MethodPost, "/migration/apply", url.Values{"db_id": {target}, "sql_script": {"SELECT 1"}}},
	}

	const (
		ok     = http.StatusOK
		denied = http.StatusForbidden
	)
	// expected status per principal, in the order of routes above
	expect := map[string][]int{
		"nogrant":      {denied, denied, denied, denied, denied, denied},
		"adminnogrant": {denied, denied, denied, denied, denied, denied},
		"viewer":       {ok, ok, ok, ok, denied, denied},
		"editor":       {ok, ok, ok, ok, ok, ok},
		"both":         {ok, ok, ok, ok, ok, ok},
	}

	for principal, statuses := range expect {
		c := newClient(t, env.srv)
		c.login(principal, "Password-"+principal)
		for i, rt := range routes {
			t.Run(fmt.Sprintf("%s/%s", principal, rt.name), func(t *testing.T) {
				var code int
				if rt.method == http.MethodGet {
					code = c.get(rt.path).Code
				} else {
					code = c.post(rt.path, rt.form).Code
				}
				if code != statuses[i] {
					t.Errorf("status %d, ingin %d", code, statuses[i])
				}
			})
		}
	}

	// No session at all: pages redirect to /login, HTMX posts get 401 + HX-Redirect.
	anon := newClient(t, env.srv)
	for _, rt := range routes {
		var code int
		if rt.method == http.MethodGet {
			code = anon.get(rt.path).Code
		} else {
			code = anon.post(rt.path, rt.form).Code
		}
		if code != http.StatusSeeOther && code != http.StatusUnauthorized {
			t.Errorf("tanpa sesi %s: status %d, ingin redirect/401", rt.name, code)
		}
	}

	// Unknown or malformed database ids are treated as "no access", never as a server error.
	editor := newClient(t, env.srv)
	editor.login("editor", "Password-editor")
	for _, bad := range []string{"db-1", "00000000-0000-0000-0000-00000000dead", "'; DROP TABLE users; --"} {
		if code := editor.get("/dashboard?db_id=" + url.QueryEscape(bad)).Code; code != denied {
			t.Errorf("db_id %q: status %d, ingin 403", bad, code)
		}
	}
}

func TestAdminRoutes_ForbiddenForUsers_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	c := loggedIn(t, env.srv, "dhia")

	if code := c.get("/admin").Code; code != http.StatusForbidden {
		t.Errorf("GET /admin oleh USER: status %d, ingin 403", code)
	}
	rec := c.post("/admin/users/role", url.Values{"user_id": {userID(t, env.srv, "dhia")}, "role": {authn.RolePlatformAdmin}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /admin/users/role oleh USER: status %d, ingin 403", rec.Code)
	}
	u, _ := env.sessions.GetUserByUsername(context.Background(), "dhia")
	if u.Role != authn.RoleUser {
		t.Fatalf("USER tidak boleh bisa menaikkan role-nya sendiri, role sekarang: %s", u.Role)
	}
}
