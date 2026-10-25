package http_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/app/internal/admin"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	apphttp "dbplatform/app/internal/http"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/internal/testutil"
)

// testAccounts are registered on every test server.
var testAccounts = []authn.RegisterInput{
	{Username: "admin", Email: "admin@email.com", Password: "SuperSecretPass999!", Role: authn.RolePlatformAdmin},
	{Username: "moreno", Email: "moreno@email.com", Password: "Rahasia123", Role: authn.RolePlatformAdmin},
	{Username: "dhia", Email: "dhia@email.com", Password: "PasswordDhia123!", Role: authn.RoleUser},
	{Username: "caca", Password: "PasswordCaca123!", Role: authn.RoleUser},
}

var testPasswords = func() map[string]string {
	m := map[string]string{}
	for _, a := range testAccounts {
		m[a.Username] = a.Password
	}
	return m
}()

// testEnv is a server on a schema-isolated platform DB (integration: skipped without TEST_DATABASE_URL).
type testEnv struct {
	srv      *apphttp.Server
	db       *sql.DB
	sessions *authn.SessionStore
	groups   *groups.Repository
	grants   *rbac.PostgresGrantStore
	targets  *dbmanager.Manager
	registry *dbmanager.ConnectionRegistry
}

func newTestEnv(t *testing.T, logOutput io.Writer) *testEnv {
	t.Helper()
	db := testutil.NewPlatformDB(t)
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	for _, acc := range testAccounts {
		if _, err := store.RegisterUser(context.Background(), acc); err != nil {
			t.Fatalf("register test account %s: %v", acc.Username, err)
		}
	}
	gr := groups.NewRepository(db)
	grants := rbac.NewPostgresGrantStore(db)
	cipher, err := dbmanager.NewCredentialCipher(testutil.CredentialsKey(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := dbmanager.NewConnectionRegistry()
	t.Cleanup(registry.Close)
	targets := dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, registry)
	srv := apphttp.NewServer(logOutput, apphttp.Deps{
		Sessions: store,
		Groups:   gr,
		Access:   rbac.NewAuthorizer(grants),
		Admin:    admin.NewService(store, gr, grants, targets),
		Registry: registry,
	})
	return &testEnv{srv: srv, db: db, sessions: store, groups: gr, grants: grants, targets: targets, registry: registry}
}

// newTestServer is newTestEnv when only the server is needed.
func newTestServer(t *testing.T, logOutput io.Writer) *apphttp.Server {
	t.Helper()
	return newTestEnv(t, logOutput).srv
}

// userID returns the UUID of a registered account.
func userID(t *testing.T, srv *apphttp.Server, username string) string {
	t.Helper()
	u, err := srv.Sessions().GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("lookup %s: %v", username, err)
	}
	return u.ID
}

// client carries the CSRF and session cookies of one browser.
type client struct {
	t       *testing.T
	srv     *apphttp.Server
	cookies map[string]*http.Cookie
}

func newClient(t *testing.T, srv *apphttp.Server) *client {
	c := &client{t: t, srv: srv, cookies: map[string]*http.Cookie{}}
	c.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if c.cookies[apphttp.CSRFCookieName] == nil {
		t.Fatalf("no CSRF cookie issued")
	}
	return c
}

func (c *client) do(req *http.Request) *httptest.ResponseRecorder {
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	if csrf := c.cookies[apphttp.CSRFCookieName]; csrf != nil {
		req.Header.Set(apphttp.CSRFHeaderName, csrf.Value)
	}
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	return rec
}

func (c *client) get(path string) *httptest.ResponseRecorder {
	return c.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (c *client) post(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return c.do(req)
}

// login signs the client in and fails the test if it does not work.
func (c *client) login(identifier, password string) {
	c.t.Helper()
	rec := c.post("/auth/login", url.Values{"identifier": {identifier}, "password": {password}})
	if rec.Header().Get("HX-Redirect") != "/dashboard" {
		c.t.Fatalf("login %s failed: %s", identifier, rec.Body.String())
	}
}

// loggedIn returns a client signed in as one of testAccounts.
func loggedIn(t *testing.T, srv *apphttp.Server, username string) *client {
	t.Helper()
	c := newClient(t, srv)
	c.login(username, testPasswords[username])
	return c
}
