package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/frontend/authn"
)

func TestFrontendSSR_LoginAndDashboardFlow(t *testing.T) {
	// Simulasi Backend API (GET /api/csrf-token, POST /api/auth/login, GET /api/auth/me)
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/csrf-token":
			http.SetCookie(w, &http.Cookie{
				Name:     "__Host-csrf",
				Value:    "test-csrf-token-123",
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"csrf_token": "test-csrf-token-123",
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			csrfCookie, err := r.Cookie("__Host-csrf")
			if err != nil || csrfCookie.Value != "test-csrf-token-123" || r.Header.Get("X-CSRF-Token") != "test-csrf-token-123" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Token CSRF tidak valid"})
				return
			}

			var req struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Username == "admin" && req.Password == "admin123" {
				http.SetCookie(w, &http.Cookie{
					Name:     "__Host-sid",
					Value:    "session-xyz",
					Path:     "/",
					HttpOnly: true,
					Secure:   true,
					SameSite: http.SameSiteLaxMode,
				})
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "login berhasil"})
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Username atau password salah!"})

		case r.Method == http.MethodGet && r.URL.Path == "/api/auth/me":
			sid, err := r.Cookie("__Host-sid")
			if err != nil || sid.Value != "session-xyz" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Sesi tidak valid"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(authn.AuthLayoutUser{
				ID:              "usr-admin",
				Username:        "admin",
				Role:            "PLATFORM_ADMIN",
				IsPlatformAdmin: true,
				Databases: []authn.DatabaseAccessSummary{
					{DatabaseID: "db-1", DatabaseName: "PostgreSQL Utama", DBMS: "POSTGRESQL", EffectiveAccess: "VIEW_EDIT"},
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockBackend.Close()

	mux := NewFrontendMux(mockBackend.URL)

	// 1. GET /login -> mengambil CSRF token dari Backend & merender halaman Login
	reqGetLogin := httptest.NewRequest(http.MethodGet, "/login", nil)
	recGetLogin := httptest.NewRecorder()
	mux.ServeHTTP(recGetLogin, reqGetLogin)

	if recGetLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /login, got %d", recGetLogin.Code)
	}
	if !strings.Contains(recGetLogin.Body.String(), "test-csrf-token-123") {
		t.Fatalf("expected CSRF token to be rendered in login page")
	}

	// 2. POST /login dengan kredensial valid + CSRF -> HX-Redirect ke /dashboard
	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "admin123")
	reqPostLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	reqPostLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPostLogin.Header.Set("X-CSRF-Token", "test-csrf-token-123")
	for _, c := range recGetLogin.Result().Cookies() {
		reqPostLogin.AddCookie(c)
	}

	recPostLogin := httptest.NewRecorder()
	mux.ServeHTTP(recPostLogin, reqPostLogin)

	if recPostLogin.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("expected HX-Redirect to /dashboard, got %q (body: %s)", recPostLogin.Header().Get("HX-Redirect"), recPostLogin.Body.String())
	}

	// 3. GET /dashboard tanpa cookie sesi -> redirect ke /login
	reqDashNoAuth := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	recDashNoAuth := httptest.NewRecorder()
	mux.ServeHTTP(recDashNoAuth, reqDashNoAuth)
	if recDashNoAuth.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on unauthenticated GET /dashboard, got %d", recDashNoAuth.Code)
	}

	// 4. GET /dashboard dengan cookie __Host-sid -> mengambil data user dari Backend /api/auth/me & merender Dashboard
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	for _, c := range recPostLogin.Result().Cookies() {
		reqDash.AddCookie(c)
	}
	recDash := httptest.NewRecorder()
	mux.ServeHTTP(recDash, reqDash)

	if recDash.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /dashboard, got %d", recDash.Code)
	}
	dashBody := recDash.Body.String()
	if !strings.Contains(dashBody, "Database Viewer") || !strings.Contains(dashBody, "PostgreSQL Utama") {
		t.Fatalf("expected dashboard layout with Database Viewer and DB list from backend")
	}
}
