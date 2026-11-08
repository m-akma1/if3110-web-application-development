package http_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAPI_HealthCheck(t *testing.T) {
	srv := newTestServer(t, nil)
	c := newClient(t, srv)

	rec := c.get("/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health status %d, want 200", rec.Code)
	}

	var data map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal health response: %v", err)
	}
	if data["status"] != "ok" {
		t.Fatalf("status %s, want ok", data["status"])
	}
}

func TestAPI_AuthFlow(t *testing.T) {
	srv := newTestServer(t, nil)
	c := newClient(t, srv)

	// 1. Unauthenticated /api/auth/me -> 401
	rec := c.get("/api/auth/me")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/auth/me unauthenticated: status %d, want 401", rec.Code)
	}

	// 2. Login with valid user
	rec = c.postJSON("/api/auth/login", `{"identifier":"bob","password":"PasswordBob123!"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/login status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 3. Authenticated /api/auth/me -> 200
	rec = c.get("/api/auth/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/me authenticated: status %d, want 200", rec.Code)
	}

	var meResp struct {
		User struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("unmarshal me response: %v", err)
	}
	if meResp.User.Username != "bob" {
		t.Fatalf("username %s, want bob", meResp.User.Username)
	}

	// 4. Logout -> 200
	rec = c.postJSON("/api/auth/logout", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/logout status %d, want 200", rec.Code)
	}

	// 5. Subsequent /api/auth/me -> 401
	rec = c.get("/api/auth/me")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/auth/me after logout: status %d, want 401", rec.Code)
	}
}

func TestAPI_AdminAccessControl(t *testing.T) {
	srv := newTestServer(t, nil)

	// Bob is a regular user -> 403
	bob := loggedIn(t, srv, "bob")
	rec := bob.get("/api/admin/overview")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /api/admin/overview as bob: status %d, want 403", rec.Code)
	}

	// Admin is platform admin -> 200
	adminClient := loggedIn(t, srv, "admin")
	rec = adminClient.get("/api/admin/overview")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/overview as admin: status %d, want 200", rec.Code)
	}
}
