package http_test

import (
	"context"
	"io"
	"testing"

	"dbplatform/app/internal/authn"
	apphttp "dbplatform/app/internal/http"
)

// testAccounts are registered on every test server (replaces the removed hard-coded seed users).
var testAccounts = []authn.RegisterInput{
	{Username: "admin", Email: "admin@email.com", Password: "SuperSecretPass999!", Role: authn.RolePlatformAdmin},
	{Username: "moreno", Email: "moreno@email.com", Password: "Rahasia123", Role: authn.RolePlatformAdmin},
	{Username: "dhia", Email: "dhia@email.com", Password: "PasswordDhia123!", Role: authn.RoleUser},
	{Username: "caca", Password: "PasswordCaca123!", Role: authn.RoleUser},
}

// newTestServer builds a server on an in-memory session store with the test accounts registered.
func newTestServer(t *testing.T, logOutput io.Writer) *apphttp.Server {
	t.Helper()
	store := authn.NewMemorySessionStore()
	for _, acc := range testAccounts {
		if _, err := store.RegisterUser(context.Background(), acc); err != nil {
			t.Fatalf("register test account %s: %v", acc.Username, err)
		}
	}
	return apphttp.NewServerWithSessions(logOutput, store)
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
