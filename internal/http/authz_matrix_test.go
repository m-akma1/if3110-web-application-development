package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/authn"
	"dbplatform/internal/migration"
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

	principals := []struct {
		name   string
		role   string
		groups []string
	}{
		{"nogrant", authn.RoleUser, nil},
		{"adminnogrant", authn.RolePlatformAdmin, nil},
		{"viewer", authn.RoleUser, []string{viewers.ID}},
		{"editor", authn.RoleUser, []string{editors.ID}},
		{"both", authn.RoleUser, []string{viewers.ID, editors.ID}},
	}
	for _, p := range principals {
		u, err := env.sessions.RegisterUser(ctx, authn.RegisterInput{
			Username: p.name,
			Password: "Password-" + p.name,
			Role:     p.role,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, gid := range p.groups {
			if err := env.groups.AddMember(ctx, gid, u.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	engine := migration.NewEngine(env.db, env.registry, migration.Limits{})
	newDraft := func() string {
		d, err := env.migrations.CreateDraft(ctx, migration.NewDraft{
			DatabaseID: target,
			CreatedBy:  userID(t, env.srv, "editor"),
			Name:       "seed",
			SQL:        "SELECT 1",
		})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	executed, _, err := engine.Execute(ctx, newDraft(), "")
	if err != nil {
		t.Fatal(err)
	}

	type route struct {
		name   string
		method string
		path   string
		form   url.Values
	}
	routesFor := func(draft string) []route {
		script := url.Values{"name": {"matrix"}, "sql_script": {"SELECT 1"}}
		return []route{
			{"viewer overview", http.MethodGet, "/api/viewer?db_id=" + target, nil},
			{"table data", http.MethodGet, "/api/viewer/table?db_id=" + target + "&name=customers", nil},
			{"read-only query", http.MethodPost, "/api/viewer/query", url.Values{"db_id": {target}, "sql_query": {"SELECT 1"}}},
			{"migration overview", http.MethodGet, "/api/migration?db_id=" + target, nil},
			{"migration detail", http.MethodGet, "/api/migration/" + executed.ID, nil},
			{"create draft", http.MethodPost, "/api/migration/drafts", url.Values{"db_id": {target}, "name": {"x"}, "sql_script": {"SELECT 1"}}},
			{"save draft", http.MethodPost, "/api/migration/" + draft, script},
			{"dry run", http.MethodPost, "/api/migration/" + draft + "/dry-run", script},
			{"execute", http.MethodPost, "/api/migration/" + draft + "/execute", script},
			{"duplicate", http.MethodPost, "/api/migration/" + executed.ID + "/duplicate", nil},
		}
	}

	const (
		ok     = http.StatusOK
		denied = http.StatusForbidden
	)
	// expected status per principal, in the order of routes above
	expect := map[string][]int{
		"nogrant":      {denied, denied, denied, denied, denied, denied, denied, denied, denied, denied},
		"adminnogrant": {denied, denied, denied, denied, denied, denied, denied, denied, denied, denied},
		"viewer":       {ok, ok, ok, ok, ok, denied, denied, denied, denied, denied},
		"editor":       {ok, ok, ok, ok, ok, ok, ok, ok, ok, ok},
		"both":         {ok, ok, ok, ok, ok, ok, ok, ok, ok, ok},
	}

	for principal, statuses := range expect {
		c := newClient(t, env.srv)
		c.login(principal, "Password-"+principal)
		for i, rt := range routesFor(newDraft()) {
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

	// No session at all: API requests get 401 Unauthorized
	anon := newClient(t, env.srv)
	for _, rt := range routesFor(newDraft()) {
		var code int
		if rt.method == http.MethodGet {
			code = anon.get(rt.path).Code
		} else {
			code = anon.post(rt.path, rt.form).Code
		}
		if code != http.StatusUnauthorized {
			t.Errorf("tanpa sesi %s: status %d, ingin 401", rt.name, code)
		}
	}

	// Unknown or malformed database ids are treated as "no access", never as a server error.
	editor := newClient(t, env.srv)
	editor.login("editor", "Password-editor")
	for _, bad := range []string{"db-1", "00000000-0000-0000-0000-00000000dead", "'; DROP TABLE users; --"} {
		if code := editor.get("/api/viewer?db_id=" + url.QueryEscape(bad)).Code; code != denied {
			t.Errorf("db_id %q: status %d, ingin 403", bad, code)
		}
	}
}

func TestAdminRoutes_ForbiddenForUsers_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	c := loggedIn(t, env.srv, "bob")

	if code := c.get("/api/admin/overview").Code; code != http.StatusForbidden {
		t.Errorf("GET /api/admin/overview oleh USER: status %d, ingin 403", code)
	}
	rec := c.post("/api/admin/users/"+userID(t, env.srv, "bob")+"/role", url.Values{"role": {authn.RolePlatformAdmin}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /api/admin/users/{id}/role oleh USER: status %d, ingin 403", rec.Code)
	}
	u, _ := env.sessions.GetUserByUsername(context.Background(), "bob")
	if u.Role != authn.RoleUser {
		t.Fatalf("USER tidak boleh bisa menaikkan role-nya sendiri, role sekarang: %s", u.Role)
	}
}
