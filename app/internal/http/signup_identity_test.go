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

// authCSRF opens a page and returns its CSRF cookie and body.
func authCSRF(t *testing.T, srv *apphttp.Server, path string) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == apphttp.CSRFCookieName && c.Value != "" {
			return c, rec.Body.String()
		}
	}
	t.Fatalf("no CSRF cookie on %s", path)
	return nil, ""
}

func postForm(srv *apphttp.Server, csrf *http.Cookie, path string, form url.Values, extra ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set(apphttp.CSRFHeaderName, csrf.Value)
	req.AddCookie(csrf)
	for _, c := range extra {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == authn.SessionCookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestAuthPages_NoAdminSignupOrLeakedCodes(t *testing.T) {
	srv := newTestServer(t, nil)
	_, signup := authCSRF(t, srv, "/signup")
	_, login := authCSRF(t, srv, "/login")

	for _, secret := range []string{"OWNER-SECRET-2026", "TEAM-BACKEND", "TEAM-ANALYST", "account_type", "Pemilik Database"} {
		if strings.Contains(signup, secret) {
			t.Errorf("halaman signup tidak boleh memuat %q", secret)
		}
	}
	if !strings.Contains(signup, `name="username"`) {
		t.Errorf("signup seharusnya memiliki field username")
	}
	if !strings.Contains(login, `name="identifier"`) || strings.Contains(login, `type="email"`) {
		t.Errorf("login seharusnya memakai satu field teks 'identifier' (username atau email)")
	}
}

func TestSignup_UsernameOnly_ThenLoginByUsername(t *testing.T) {
	srv := newTestServer(t, nil)
	csrf, _ := authCSRF(t, srv, "/signup")

	rec := postForm(srv, csrf, "/signup", url.Values{"username": {"Tanpa.Email"}, "password": {"PasswordTanpa123"}})
	if rec.Header().Get("HX-Redirect") != "/dashboard" || sessionCookie(rec) == nil {
		t.Fatalf("signup hanya dengan username (tanpa email & kode) seharusnya berhasil, body: %s", rec.Body.String())
	}

	login := postForm(srv, csrf, "/login", url.Values{"identifier": {"tanpa.email"}, "password": {"PasswordTanpa123"}})
	if login.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("login via username seharusnya berhasil, body: %s", login.Body.String())
	}
}

func TestSignup_ValidationAndDuplicates(t *testing.T) {
	srv := newTestServer(t, nil)
	csrf, _ := authCSRF(t, srv, "/signup")

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"username kosong", url.Values{"password": {"PasswordValid123"}}, "Username wajib diisi"},
		{"username tidak valid", url.Values{"username": {"a b"}, "password": {"PasswordValid123"}}, "3-32 karakter"},
		{"username dipakai (beda huruf)", url.Values{"username": {"BOB"}, "password": {"PasswordValid123"}}, "Username sudah terdaftar"},
		{"email dipakai", url.Values{"username": {"bob2"}, "email": {"BOB@email.com"}, "password": {"PasswordValid123"}}, "Email sudah terdaftar"},
		{"email tidak valid", url.Values{"username": {"baru1"}, "email": {"bukan-email"}, "password": {"PasswordValid123"}}, "Format email tidak valid"},
		{"password pendek", url.Values{"username": {"baru2"}, "password": {"pendek"}}, "8-128 karakter"},
		{"kode tim salah", url.Values{"username": {"baru3"}, "password": {"PasswordValid123"}, "invite_code": {"KODE-SALAH"}}, "Kode tim tidak valid"},
	}
	for _, tc := range cases {
		rec := postForm(srv, csrf, "/signup", tc.form)
		if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: seharusnya ditolak dengan pesan %q", tc.name, tc.want)
		}
	}
}

func TestSignup_AccountTypeIsIgnored(t *testing.T) {
	srv := newTestServer(t, nil)
	csrf, _ := authCSRF(t, srv, "/signup")

	rec := postForm(srv, csrf, "/signup", url.Values{
		"username":     {"calon.admin"},
		"password":     {"PasswordCalon123"},
		"account_type": {authn.RolePlatformAdmin},
		"invite_code":  {"OWNER-SECRET-2026"},
	})
	if rec.Header().Get("HX-Redirect") != "" {
		t.Fatalf("passcode pemilik lama tidak boleh diterima sebagai kode")
	}

	rec = postForm(srv, csrf, "/signup", url.Values{
		"username":     {"calon.admin"},
		"password":     {"PasswordCalon123"},
		"account_type": {authn.RolePlatformAdmin},
	})
	sid := sessionCookie(rec)
	if sid == nil {
		t.Fatalf("signup seharusnya berhasil, body: %s", rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(sid)
	adminRec := httptest.NewRecorder()
	srv.ServeHTTP(adminRec, req)
	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("akun hasil signup harus USER (403 di /admin), dapat: %d", adminRec.Code)
	}
}

func TestLogin_ByEmailOrUsername_AndGenericError(t *testing.T) {
	srv := newTestServer(t, nil)
	csrf, _ := authCSRF(t, srv, "/login")

	for _, id := range []string{"bob", "BOB", "bob@email.com"} {
		rec := postForm(srv, csrf, "/login", url.Values{"identifier": {id}, "password": {"PasswordBob123!"}})
		if rec.Header().Get("HX-Redirect") != "/dashboard" {
			t.Errorf("login dengan %q seharusnya berhasil", id)
		}
	}

	// Pesan error sama untuk user tidak ada dan password salah (tidak membocorkan keberadaan akun)
	unknown := postForm(srv, csrf, "/login", url.Values{"identifier": {"tidak.ada"}, "password": {"PasswordBob123!"}})
	wrong := postForm(srv, csrf, "/login", url.Values{"identifier": {"bob"}, "password": {"PasswordSalah123"}})
	for _, rec := range []*httptest.ResponseRecorder{unknown, wrong} {
		if rec.Header().Get("HX-Redirect") != "" || !strings.Contains(rec.Body.String(), "Username/email atau password tidak valid") {
			t.Errorf("login gagal seharusnya menampilkan pesan generik, body: %s", rec.Body.String())
		}
	}
}
