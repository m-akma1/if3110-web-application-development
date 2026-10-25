package http_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/app/internal/authn"
	apphttp "dbplatform/app/internal/http"
)

// getSignupCSRF membuka halaman signup dan mengembalikan cookie CSRF beserta body halaman
func getSignupCSRF(t *testing.T, srv *apphttp.Server) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/signup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("gagal membuka /auth/signup, status: %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == apphttp.CSRFCookieName && c.Value != "" {
			return c, rec.Body.String()
		}
	}
	t.Fatalf("cookie CSRF tidak ditemukan pada /auth/signup")
	return nil, ""
}

func postSignup(srv *apphttp.Server, csrf *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(apphttp.CSRFHeaderName, csrf.Value)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// Halaman signup tidak boleh membocorkan passcode admin maupun kode undangan tim
func TestSignupPage_DoesNotLeakCodes(t *testing.T) {
	srv := apphttp.NewServer(nil)
	_, body := getSignupCSRF(t, srv)

	for _, secret := range []string{"OWNER-SECRET-2026", "TEAM-BACKEND", "TEAM-ANALYST", "account_type"} {
		if strings.Contains(body, secret) {
			t.Fatalf("halaman signup tidak boleh memuat %q", secret)
		}
	}
}

// Passcode pemilik lama tidak lagi diterima sebagai kode apa pun
func TestSignup_OldOwnerPasscodeRejected(t *testing.T) {
	srv := apphttp.NewServer(nil)
	csrf, _ := getSignupCSRF(t, srv)

	form := url.Values{}
	form.Set("email", "penyusup@email.com")
	form.Set("password", "PasswordPenyusup123!")
	form.Set("account_type", authn.RolePlatformAdmin)
	form.Set("invite_code", "OWNER-SECRET-2026")

	rec := postSignup(srv, csrf, form)
	if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), "Kode tim tidak valid") {
		t.Fatalf("passcode pemilik lama seharusnya ditolak, body: %s", rec.Body.String())
	}
}

// Mengirim account_type=PLATFORM_ADMIN secara manual tetap menghasilkan akun USER (tidak bisa membuka /admin)
func TestSignup_CannotSelfRegisterAsPlatformAdmin(t *testing.T) {
	srv := apphttp.NewServer(nil)
	csrf, _ := getSignupCSRF(t, srv)

	form := url.Values{}
	form.Set("email", "calonadmin@email.com")
	form.Set("password", "PasswordCalon123!")
	form.Set("account_type", authn.RolePlatformAdmin)
	form.Set("invite_code", "TEAM-BACKEND")

	rec := postSignup(srv, csrf, form)
	if rec.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("signup dengan kode tim valid seharusnya berhasil, body: %s", rec.Body.String())
	}

	var sid *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authn.SessionCookieName && c.Value != "" {
			sid = c
		}
	}
	if sid == nil {
		t.Fatalf("cookie sesi tidak diterbitkan setelah signup")
	}

	reqAdmin := httptest.NewRequest(http.MethodGet, "/admin", nil)
	reqAdmin.AddCookie(sid)
	recAdmin := httptest.NewRecorder()
	srv.ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusForbidden {
		t.Fatalf("akun hasil signup seharusnya USER dan ditolak dari /admin dengan 403, dapat: %d", recAdmin.Code)
	}
}
