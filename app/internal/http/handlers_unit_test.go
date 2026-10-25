package http_test

// Unit tests for the HTTP layer: no database. Identity uses the in-memory session store with a
// fake clock, authorization uses rbac.Authorizer over a stub GrantStore, and the connection
// registry is empty (no target is connected). Everything here runs in `make test`.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"dbplatform/app/internal/authn"
	apphttp "dbplatform/app/internal/http"
	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/views/components"
)

// stubGrants is a GrantStore: userID -> grants (one per group the user is in).
type stubGrants struct {
	grants    map[string][]rbac.Grant
	err       error // every call fails
	levelsErr error // only the per-database check fails
}

func (s *stubGrants) LevelsForUser(_ context.Context, userID, dbID string) ([]rbac.AccessLevel, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.levelsErr != nil {
		return nil, s.levelsErr
	}
	var out []rbac.AccessLevel
	for _, g := range s.grants[userID] {
		if g.DatabaseID == dbID {
			out = append(out, g.Level)
		}
	}
	return out, nil
}

func (s *stubGrants) GrantsForUser(_ context.Context, userID string) ([]rbac.Grant, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.grants[userID], nil
}

type unitEnv struct {
	srv    *apphttp.Server
	store  *authn.SessionStore
	grants *stubGrants
	logs   *bytes.Buffer
	now    time.Time
	dbID   string // a registered database with no live connection
}

// unit principals: password is "Password-" + name.
var unitPrincipals = map[string]string{
	"nogrant": authn.RoleUser,
	"viewer":  authn.RoleUser,
	"editor":  authn.RoleUser,
	"both":    authn.RoleUser,          // VIEW_ONLY and VIEW_EDIT through two groups
	"admin":   authn.RolePlatformAdmin, // no group grant: admin is not a database access level
}

func newUnitEnv(t *testing.T) *unitEnv {
	t.Helper()
	env := &unitEnv{
		store:  authn.NewMemorySessionStore(),
		grants: &stubGrants{grants: map[string][]rbac.Grant{}},
		logs:   &bytes.Buffer{},
		now:    time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		dbID:   ids.New(),
	}
	env.store.SetClock(func() time.Time { return env.now })

	ctx := context.Background()
	for name, role := range unitPrincipals {
		u, err := env.store.RegisterUser(ctx, authn.RegisterInput{Username: name, Password: "Password-" + name, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		grant := func(level rbac.AccessLevel) {
			env.grants.grants[u.ID] = append(env.grants.grants[u.ID], rbac.Grant{DatabaseID: env.dbID, DatabaseName: "Demo", Level: level})
		}
		switch name {
		case "viewer":
			grant(rbac.AccessViewOnly)
		case "editor":
			grant(rbac.AccessViewEdit)
		case "both":
			grant(rbac.AccessViewOnly)
			grant(rbac.AccessViewEdit)
		}
	}

	env.srv = apphttp.NewServer(env.logs, apphttp.Deps{
		Sessions: env.store,
		Access:   rbac.NewAuthorizer(env.grants),
	})
	return env
}

func (env *unitEnv) login(t *testing.T, name string) *client {
	t.Helper()
	c := newClient(t, env.srv)
	c.login(name, "Password-"+name)
	return c
}

func (c *client) status(method, path string, form url.Values) *httptest.ResponseRecorder {
	if method == http.MethodGet {
		return c.get(path)
	}
	return c.post(path, form)
}

// Viewer routes × principals, decided by rbac alone. Allowed principals reach the handler and
// get the "database unavailable" state, because nothing is connected.
func TestViewerRoutes_AccessMatrix_Unit(t *testing.T) {
	env := newUnitEnv(t)
	routes := []struct {
		name, method, path string
		form               url.Values
	}{
		{"page", http.MethodGet, components.ViewerURL(env.dbID), nil},
		{"table", http.MethodGet, components.TableURL(env.dbID, "public", "customers", 1), nil},
		{"query", http.MethodPost, "/dashboard/query", url.Values{"db_id": {env.dbID}, "sql_query": {"SELECT 1"}}},
	}
	want := map[string]int{
		"nogrant": http.StatusForbidden,
		"admin":   http.StatusForbidden,
		"viewer":  http.StatusServiceUnavailable,
		"editor":  http.StatusServiceUnavailable,
		"both":    http.StatusServiceUnavailable,
	}
	for name, code := range want {
		c := env.login(t, name)
		for _, rt := range routes {
			rec := c.status(rt.method, rt.path, rt.form)
			if rec.Code != code {
				t.Errorf("%s %s: status %d, want %d", name, rt.name, rec.Code, code)
			}
			body := rec.Body.String()
			if code == http.StatusForbidden && !strings.Contains(body, "Akses ditolak") {
				t.Errorf("%s %s: denial should explain itself", name, rt.name)
			}
			if code == http.StatusServiceUnavailable && !strings.Contains(body, "Database tidak tersedia") {
				t.Errorf("%s %s: expected the unavailable state", name, rt.name)
			}
		}
	}

	// Unknown or malformed database ids are no access, for users who do have other grants.
	viewer := env.login(t, "viewer")
	for _, bad := range []string{ids.New(), "db-1", "'; DROP TABLE x; --"} {
		if code := viewer.get(components.ViewerURL(bad)).Code; code != http.StatusForbidden {
			t.Errorf("db_id %q: status %d, want 403", bad, code)
		}
	}

	// No session: pages redirect, HTMX requests get 401 with HX-Redirect.
	anon := newClient(t, env.srv)
	if rec := anon.get(components.ViewerURL(env.dbID)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("anonymous page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := anon.post("/dashboard/query", routes[2].form); rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/login" {
		t.Errorf("anonymous HTMX query: %d %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

// A failure while reading grants denies access; it never falls back to showing anything.
func TestViewer_GrantStoreFailureDenies_Unit(t *testing.T) {
	env := newUnitEnv(t)
	c := env.login(t, "viewer")

	// The per-database check fails.
	env.grants.levelsErr = errors.New("platform db down")
	for _, rec := range []*httptest.ResponseRecorder{
		c.get(components.TableURL(env.dbID, "public", "customers", 1)),
		c.post("/dashboard/query", url.Values{"db_id": {env.dbID}, "sql_query": {"SELECT 1"}}),
		c.get(components.ViewerURL(env.dbID)),
	} {
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Gagal memeriksa hak akses") {
			t.Errorf("grant check failure: %d %s", rec.Code, rec.Body.String())
		}
	}

	// Even the user's profile (their database list) cannot be loaded.
	env.grants.err = errors.New("platform db down")
	if rec := c.get(components.ViewerURL(env.dbID)); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "Demo") {
		t.Errorf("profile failure: status %d", rec.Code)
	}
}

// Input problems are answered with 422 before any database is touched.
func TestViewer_InputValidation_Unit(t *testing.T) {
	env := newUnitEnv(t)
	c := env.login(t, "viewer")

	if rec := c.get("/dashboard/table?db_id=" + env.dbID); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing table name: status %d, want 422", rec.Code)
	}
	cases := map[string]string{
		"DELETE FROM customers":                 "DELETE",
		"SELECT 1; DROP TABLE customers":        ";",
		"SELECT pg_cancel_backend(1)":           "pg_cancel_backend",
		"   ":                                   "kosong",
		"WITH x AS (UPDATE t SET a=1) SELECT 1": "UPDATE",
	}
	for q, want := range cases {
		rec := c.post("/dashboard/query", url.Values{"db_id": {env.dbID}, "sql_query": {q}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%q: status %d, want 422 mentioning %q: %s", q, rec.Code, want, rec.Body.String())
		}
	}
}

func TestCSRFGuard_Unit(t *testing.T) {
	env := newUnitEnv(t)
	post := func(cookie, header string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("not json"))
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: apphttp.CSRFCookieName, Value: cookie})
		}
		if header != "" {
			req.Header.Set(apphttp.CSRFHeaderName, header)
		}
		rec := httptest.NewRecorder()
		env.srv.ServeHTTP(rec, req)
		return rec.Code
	}
	cases := []struct {
		name           string
		cookie, header string
		want           int
	}{
		{"no cookie, no header", "", "", http.StatusForbidden},
		{"header only", "", "tok", http.StatusForbidden},
		{"cookie only", "tok", "", http.StatusForbidden},
		{"mismatch", "tok-a", "tok-b", http.StatusForbidden},
		{"match reaches the handler", "tok", "tok", http.StatusBadRequest}, // the body is not JSON
	}
	for _, tc := range cases {
		if got := post(tc.cookie, tc.header); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}
	// Safe methods are not checked.
	rec := httptest.NewRecorder()
	env.srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET without CSRF token: status %d", rec.Code)
	}
}

// The session policy, seen from the browser: idle timeout, rotation with a grace period for
// the old cookie, and an absolute lifetime that rotation does not extend.
func TestSessionPolicy_OverHTTP_Unit(t *testing.T) {
	env := newUnitEnv(t)
	ok := func(c *client) bool { return c.get("/api/auth/me").Code == http.StatusOK }

	t.Run("idle timeout", func(t *testing.T) {
		c := env.login(t, "viewer")
		env.now = env.now.Add(29 * time.Minute)
		if !ok(c) {
			t.Fatal("29 minutes idle should still be valid")
		}
		env.now = env.now.Add(31 * time.Minute)
		rec := c.get(components.ViewerURL(env.dbID))
		if rec.Code != http.StatusSeeOther || c.cookies[authn.SessionCookieName] != nil {
			t.Fatalf("after 31 idle minutes: status %d, cookie kept=%v", rec.Code, c.cookies[authn.SessionCookieName] != nil)
		}
	})

	t.Run("rotation and grace period", func(t *testing.T) {
		c := env.login(t, "viewer")
		old := c.cookies[authn.SessionCookieName].Value
		env.now = env.now.Add(16 * time.Minute)
		if !ok(c) {
			t.Fatal("request after the rotation interval should succeed")
		}
		rotated := c.cookies[authn.SessionCookieName].Value
		if rotated == old {
			t.Fatal("session id should rotate after 15 minutes")
		}

		stale := newClient(t, env.srv)
		stale.cookies[authn.SessionCookieName] = &http.Cookie{Name: authn.SessionCookieName, Value: old}
		env.now = env.now.Add(10 * time.Second)
		if !ok(stale) {
			t.Fatal("the old id should work during the 15 s grace period (parallel requests)")
		}
		stale.cookies[authn.SessionCookieName] = &http.Cookie{Name: authn.SessionCookieName, Value: old}
		env.now = env.now.Add(10 * time.Second)
		if ok(stale) {
			t.Fatal("the old id must be refused after the grace period")
		}
		if !ok(c) {
			t.Fatal("the rotated id stays valid")
		}
	})

	t.Run("absolute lifetime", func(t *testing.T) {
		c := env.login(t, "viewer")
		start := env.now
		for env.now.Sub(start) < 12*time.Hour-10*time.Minute {
			env.now = env.now.Add(10 * time.Minute)
			if !ok(c) {
				t.Fatalf("active session refused after %s", env.now.Sub(start))
			}
		}
		env.now = start.Add(12*time.Hour + time.Minute)
		if ok(c) {
			t.Fatal("rotation must not extend the 12 h absolute lifetime")
		}
	})
}

func TestJSONAuthAPI_Failures_Unit(t *testing.T) {
	env := newUnitEnv(t)
	c := newClient(t, env.srv)
	postJSON := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return c.do(req).Code
	}

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"wrong password", "/api/auth/login", `{"username":"viewer","password":"nope-nope"}`, http.StatusUnauthorized},
		{"unknown user", "/api/auth/login", `{"username":"ghost","password":"Password-ghost"}`, http.StatusUnauthorized},
		{"missing password", "/api/auth/login", `{"username":"viewer"}`, http.StatusBadRequest},
		{"bad json", "/api/auth/login", `{"username":`, http.StatusBadRequest},
		{"password in url", "/api/auth/login?password=Password-viewer", `{"username":"viewer","password":"Password-viewer"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if got := postJSON(tc.path, tc.body); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	if code := c.get("/api/auth/me").Code; code != http.StatusUnauthorized {
		t.Errorf("/api/auth/me without a session: status %d", code)
	}
	c.cookies[authn.SessionCookieName] = &http.Cookie{Name: authn.SessionCookieName, Value: "forged-session-id"}
	if code := c.get("/api/auth/me").Code; code != http.StatusUnauthorized {
		t.Errorf("/api/auth/me with a forged id: status %d", code)
	}

	// Deactivated accounts cannot log in, even with the right password.
	if code := postJSON("/api/auth/login", `{"username":"viewer","password":"Password-viewer"}`); code != http.StatusOK {
		t.Fatalf("valid login: status %d", code)
	}
	u, _ := env.store.GetUserByUsername(context.Background(), "viewer")
	if err := env.store.RevokeUserAccess(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	if code := c.get("/api/auth/me").Code; code != http.StatusUnauthorized {
		t.Errorf("session of a deactivated user: status %d", code)
	}
	if code := postJSON("/api/auth/login", `{"username":"viewer","password":"Password-viewer"}`); code != http.StatusUnauthorized {
		t.Errorf("login of a deactivated user: status %d", code)
	}
}

// Request logs carry method, path, status and duration, but never passwords or session ids.
func TestRequestLog_FormatAndRedaction_Unit(t *testing.T) {
	env := newUnitEnv(t)
	c := env.login(t, "viewer")
	sid := c.cookies[authn.SessionCookieName].Value
	c.get("/dashboard/table?db_id=" + env.dbID + "&password=Hunter2-secret&session_id=" + sid)

	logs := env.logs.String()
	line := regexp.MustCompile(`\[HTTP\] method=GET path=/dashboard/table\?\S*password=\[REDACTED\]\S* status=\d{3} duration=\S+`)
	if !line.MatchString(logs) {
		t.Errorf("log line format: %s", logs)
	}
	for _, secret := range []string{"Hunter2-secret", "Password-viewer", sid} {
		if strings.Contains(logs, secret) {
			t.Errorf("log leaks %q", secret)
		}
	}
}
