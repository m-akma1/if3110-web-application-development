package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apphttp "dbplatform/app/internal/http"
)

// 1. Menguji fungsi SanitizeURL secara terisolasi
func TestSanitizeURL_Unit(t *testing.T) {
	rawURL, _ := url.Parse("/api/auth/login?username=budi&password=Rahasia123&session_id= tokenAcak999")
	sanitized := apphttp.SanitizeURL(rawURL)

	if strings.Contains(sanitized, "Rahasia123") {
		t.Fatalf("password bocor pada URL yang disanitasi: %s", sanitized)
	}
	if strings.Contains(sanitized, "tokenAcak999") {
		t.Fatalf("session_id bocor pada URL yang disanitasi: %s", sanitized)
	}
	if !strings.Contains(sanitized, "password=[REDACTED]") {
		t.Fatalf("parameter password seharusnya diubah menjadi [REDACTED], dapat: %s", sanitized)
	}
	fmt.Print("dapat : " + sanitized)
}

// 2.  Menguji CSRF Guard & Handler Tanpa HTTP Server
func TestCSRFGuard_BlocksPostWithoutToken(t *testing.T) {
	var logBuf bytes.Buffer
	srv := newTestServer(t, &logBuf)

	// Kirim POST tanpa Cookie & Header CSRF
	body := strings.NewReader(`{"username":"admin","password":"SecretPassword123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	rec := httptest.NewRecorder()

	// Panggil handler secara langsung tanpa menyalakan HTTP Server
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("seharusnya POST tanpa CSRF ditolak dengan 403 Forbidden, dapat: %d", rec.Code)
	}
}

// 3.  Alur Lengkap (Ambil CSRF -> POST Login -> Cek Sanitasi Log & Cookie)
func TestLoginFlow_CSRFAndSanitizedLog_Integration(t *testing.T) {
	var logBuf bytes.Buffer
	srv := newTestServer(t, &logBuf)

	//  Ambil Token CSRF (GET /api/csrf-token)
	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/csrf-token", nil)
	recCSRF := httptest.NewRecorder()
	srv.ServeHTTP(recCSRF, reqCSRF)

	if recCSRF.Code != http.StatusOK {
		t.Fatalf("gagal mengambil CSRF token, status: %d", recCSRF.Code)
	}

	// Ekstrak Cookie CSRF dan Body JSON
	var csrfResp map[string]string
	_ = json.NewDecoder(recCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]
	cookies := recCSRF.Result().Cookies()

	// Lakukan POST Login dengan menyertakan Cookie & Header CSRF
	secretPassword := "SuperSecretPass999!"
	loginPayload := `{"username":"admin","password":"` + secretPassword + `"}`

	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginPayload))
	reqLogin.Header.Set("Content-Type", "application/json")
	reqLogin.Header.Set(apphttp.CSRFHeaderName, csrfToken)
	for _, c := range cookies {
		reqLogin.AddCookie(c)
	}

	recLogin := httptest.NewRecorder()
	srv.ServeHTTP(recLogin, reqLogin)

	// Verifikasi login berhasil
	if recLogin.Code != http.StatusOK {
		t.Fatalf("seharusnya login dengan CSRF valid berhasil (200 OK), dapat: %d - body: %s", recLogin.Code, recLogin.Body.String())
	}

	// Verifikasi Keamanan Session Cookie (__Host-sid)
	var sessionCookie *http.Cookie
	for _, c := range recLogin.Result().Cookies() {
		if c.Name == "__Host-sid" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("cookie session __Host-sid tidak ditemukan pada response")
	}
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("atribut keamanan cookie session belum lengkap (wajib HttpOnly, Secure, SameSite)")
	}

	//  Verifikasi Password dan Session Identifier TIDAK BOCOR di Response Body maupun Log Aplikasi
	responseBody := recLogin.Body.String()
	logOutput := logBuf.String()

	if strings.Contains(responseBody, secretPassword) || strings.Contains(responseBody, sessionCookie.Value) {
		t.Fatalf("kebocoran data sensitif pada response body: %s", responseBody)
	}
	if strings.Contains(logOutput, secretPassword) || strings.Contains(logOutput, sessionCookie.Value) {
		t.Fatalf("kebocoran password atau session identifier pada log aplikasi: %s", logOutput)
	}

	// Verifikasi GET /api/auth/me mengembalikan profil AuthLayoutUser dari sesi aktif
	reqMe := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	reqMe.AddCookie(sessionCookie)
	recMe := httptest.NewRecorder()
	srv.ServeHTTP(recMe, reqMe)

	if recMe.Code != http.StatusOK {
		t.Fatalf("seharusnya GET /api/auth/me dengan sesi valid berhasil (200 OK), dapat: %d", recMe.Code)
	}

	var meResp map[string]any
	_ = json.NewDecoder(recMe.Body).Decode(&meResp)
	if meResp["username"] != "admin" {
		t.Fatalf("seharusnya username pada /api/auth/me adalah 'admin', dapat: %v", meResp["username"])
	}
}

// 4. Menguji Alur UI GOTTH (GET /login -> POST /login via HTMX -> GET /dashboard)
func TestGOTTH_LoginAndDashboardFlow(t *testing.T) {
	var logBuf bytes.Buffer
	srv := newTestServer(t, &logBuf)

	// 1. GET /login -> menerbitkan cookie __Host-csrf dan merender halaman Login
	reqGetLogin := httptest.NewRequest(http.MethodGet, "/login", nil)
	recGetLogin := httptest.NewRecorder()
	srv.ServeHTTP(recGetLogin, reqGetLogin)

	if recGetLogin.Code != http.StatusOK {
		t.Fatalf("gagal membuka halaman /login, status: %d", recGetLogin.Code)
	}

	var csrfCookie *http.Cookie
	for _, c := range recGetLogin.Result().Cookies() {
		if c.Name == apphttp.CSRFCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("cookie CSRF %s tidak ditemukan pada halaman /login", apphttp.CSRFCookieName)
	}

	// 2. POST /login via HTMX form-urlencoded
	form := url.Values{}
	form.Set("username", "Moreno")
	form.Set("password", "Rahasia123")
	reqPostLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	reqPostLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPostLogin.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqPostLogin.AddCookie(csrfCookie)

	recPostLogin := httptest.NewRecorder()
	srv.ServeHTTP(recPostLogin, reqPostLogin)

	if recPostLogin.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("seharusnya POST /login mengirim header HX-Redirect: /dashboard, dapat: %q", recPostLogin.Header().Get("HX-Redirect"))
	}

	// 3. GET /dashboard dengan cookie __Host-sid -> merender komponen Dashboard, Navbar, dan Sidebar
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDash.AddCookie(csrfCookie)
	for _, c := range recPostLogin.Result().Cookies() {
		reqDash.AddCookie(c)
	}
	recDash := httptest.NewRecorder()
	srv.ServeHTTP(recDash, reqDash)

	if recDash.Code != http.StatusOK {
		t.Fatalf("seharusnya GET /dashboard dengan sesi valid berhasil (200 OK), dapat: %d", recDash.Code)
	}
	body := recDash.Body.String()
	if !strings.Contains(body, "Database Viewer") || !strings.Contains(body, "moreno") {
		t.Fatalf("halaman /dashboard tidak memuat konten user atau komponen Database Viewer")
	}
	if !strings.Contains(body, "X-CSRF-Token") || !strings.Contains(body, csrfCookie.Value) {
		t.Fatalf("halaman /dashboard wajib menyertakan hx-headers X-CSRF-Token agar tombol Logout HTMX berfungsi")
	}

	// 4. POST /logout via HTMX menggunakan akun Moreno
	reqLogout := httptest.NewRequest(http.MethodPost, "/logout", nil)
	reqLogout.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqLogout.AddCookie(csrfCookie)
	for _, c := range recPostLogin.Result().Cookies() {
		reqLogout.AddCookie(c)
	}
	recLogout := httptest.NewRecorder()
	srv.ServeHTTP(recLogout, reqLogout)

	if recLogout.Code != http.StatusOK || recLogout.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("seharusnya POST /logout akun Moreno berhasil dan mengembalikan HX-Redirect: /login, status=%d, redirect=%q",
			recLogout.Code, recLogout.Header().Get("HX-Redirect"))
	}

	// 5. Pastikan sesi Moreno langsung nonaktif di backend setelah logout
	reqDashAfterLogout := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	for _, c := range recPostLogin.Result().Cookies() {
		reqDashAfterLogout.AddCookie(c)
	}
	recDashAfterLogout := httptest.NewRecorder()
	srv.ServeHTTP(recDashAfterLogout, reqDashAfterLogout)

	if recDashAfterLogout.Code != http.StatusSeeOther {
		t.Fatalf("seharusnya akses /dashboard dengan cookie lama setelah logout ditolak (303 Redirect ke /login), dapat: %d",
			recDashAfterLogout.Code)
	}
}

// 5. Menguji Alur Sign Up & Login menggunakan Email dan Password (/auth/signup & /auth/login)
func TestGOTTH_EmailSignupAndLoginFlow(t *testing.T) {
	var logBuf bytes.Buffer
	srv := newTestServer(t, &logBuf)

	// 1. GET /auth/signup
	reqGetSignup := httptest.NewRequest(http.MethodGet, "/auth/signup", nil)
	recGetSignup := httptest.NewRecorder()
	srv.ServeHTTP(recGetSignup, reqGetSignup)

	if recGetSignup.Code != http.StatusOK {
		t.Fatalf("gagal membuka halaman /auth/signup, status: %d", recGetSignup.Code)
	}

	var csrfCookie *http.Cookie
	for _, c := range recGetSignup.Result().Cookies() {
		if c.Name == apphttp.CSRFCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("cookie CSRF tidak ditemukan pada /auth/signup")
	}

	// 2. POST /auth/signup dengan kode tim salah -> wajib ditolak
	badSignup := url.Values{}
	badSignup.Set("email", "userbaru@email.com")
	badSignup.Set("password", "PasswordBaru123!")
	badSignup.Set("account_type", "USER")
	badSignup.Set("invite_code", "KODE-SALAH")
	reqBadSignup := httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(badSignup.Encode()))
	reqBadSignup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqBadSignup.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqBadSignup.AddCookie(csrfCookie)

	recBadSignup := httptest.NewRecorder()
	srv.ServeHTTP(recBadSignup, reqBadSignup)
	if recBadSignup.Header().Get("HX-Redirect") != "" || !strings.Contains(recBadSignup.Body.String(), "Kode tim tidak valid") {
		t.Fatalf("seharusnya signup dengan kode tim salah ditolak, body: %s", recBadSignup.Body.String())
	}

	// 3. POST /auth/signup dengan email, password, dan kode tim valid (TEAM-BACKEND)
	signupForm := url.Values{}
	signupForm.Set("username", "userbaru")
	signupForm.Set("email", "userbaru@email.com")
	signupForm.Set("password", "PasswordBaru123!")
	signupForm.Set("account_type", "USER")
	signupForm.Set("invite_code", "TEAM-BACKEND")
	reqPostSignup := httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(signupForm.Encode()))
	reqPostSignup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPostSignup.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqPostSignup.AddCookie(csrfCookie)

	recPostSignup := httptest.NewRecorder()
	srv.ServeHTTP(recPostSignup, reqPostSignup)

	if recPostSignup.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("seharusnya POST /auth/signup mengirim HX-Redirect: /dashboard, dapat: %q (body: %s)",
			recPostSignup.Header().Get("HX-Redirect"), recPostSignup.Body.String())
	}

	// 4. POST /auth/login menggunakan email dan password yang baru didaftarkan
	loginForm := url.Values{}
	loginForm.Set("email", "userbaru@email.com")
	loginForm.Set("password", "PasswordBaru123!")
	reqPostLogin := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(loginForm.Encode()))
	reqPostLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPostLogin.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqPostLogin.AddCookie(csrfCookie)

	recPostLogin := httptest.NewRecorder()
	srv.ServeHTTP(recPostLogin, reqPostLogin)

	if recPostLogin.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("seharusnya POST /auth/login dengan email berhasil dan mengirim HX-Redirect: /dashboard, dapat: %q",
			recPostLogin.Header().Get("HX-Redirect"))
	}

	// 5. Uji Read-Only Query Guard di /dashboard/query (menolak DELETE, mengizinkan SELECT)
	queryMut := url.Values{}
	queryMut.Set("sql_query", "DELETE FROM users WHERE id = '1';")
	reqMut := httptest.NewRequest(http.MethodPost, "/dashboard/query", strings.NewReader(queryMut.Encode()))
	reqMut.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqMut.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqMut.AddCookie(csrfCookie)
	for _, c := range recPostLogin.Result().Cookies() {
		reqMut.AddCookie(c)
	}
	recMut := httptest.NewRecorder()
	srv.ServeHTTP(recMut, reqMut)
	if !strings.Contains(recMut.Body.String(), "DELETE") || !strings.Contains(recMut.Body.String(), "ditolak") {
		t.Fatalf("seharusnya query DELETE ditolak oleh Read-Only Guard, dapat: %s", recMut.Body.String())
	}

	querySel := url.Values{}
	querySel.Set("sql_query", "SELECT id, username FROM users;")
	reqSel := httptest.NewRequest(http.MethodPost, "/dashboard/query", strings.NewReader(querySel.Encode()))
	reqSel.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSel.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqSel.AddCookie(csrfCookie)
	for _, c := range recPostLogin.Result().Cookies() {
		reqSel.AddCookie(c)
	}
	recSel := httptest.NewRecorder()
	srv.ServeHTTP(recSel, reqSel)
	if !strings.Contains(recSel.Body.String(), "Query Read-Only berhasil dieksekusi") {
		t.Fatalf("seharusnya query SELECT diizinkan oleh Read-Only Guard, dapat: %s", recSel.Body.String())
	}

	// 6. User biasa (USER) mengakses /admin -> wajib ditolak 403 Forbidden
	reqUserAdmin := httptest.NewRequest(http.MethodGet, "/admin", nil)
	for _, c := range recPostLogin.Result().Cookies() {
		reqUserAdmin.AddCookie(c)
	}
	recUserAdmin := httptest.NewRecorder()
	srv.ServeHTTP(recUserAdmin, reqUserAdmin)
	if recUserAdmin.Code != http.StatusForbidden {
		t.Fatalf("seharusnya USER biasa ditolak saat membuka /admin (403), dapat: %d", recUserAdmin.Code)
	}

	// 7. Login sebagai PLATFORM_ADMIN (Moreno) dan akses /admin + ubah hak akses user
	adminForm := url.Values{}
	adminForm.Set("email", "moreno@email.com")
	adminForm.Set("password", "Rahasia123")
	reqAdminLogin := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(adminForm.Encode()))
	reqAdminLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAdminLogin.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqAdminLogin.AddCookie(csrfCookie)
	recAdminLogin := httptest.NewRecorder()
	srv.ServeHTTP(recAdminLogin, reqAdminLogin)

	reqAdminPage := httptest.NewRequest(http.MethodGet, "/admin", nil)
	reqAdminPage.AddCookie(csrfCookie)
	for _, c := range recAdminLogin.Result().Cookies() {
		reqAdminPage.AddCookie(c)
	}
	recAdminPage := httptest.NewRecorder()
	srv.ServeHTTP(recAdminPage, reqAdminPage)
	if recAdminPage.Code != http.StatusOK || !strings.Contains(recAdminPage.Body.String(), "TEAM-BACKEND") {
		t.Fatalf("seharusnya PLATFORM_ADMIN dapat membuka /admin dan melihat kode TEAM-BACKEND, status=%d", recAdminPage.Code)
	}

	// 8. Admin mengubah hak akses user dhia melalui POST /admin/users/access
	dhiaID := userID(t, srv, "dhia")
	grantForm := url.Values{}
	grantForm.Set("user_id", dhiaID)
	grantForm.Set("role", "USER")
	grantForm.Set("access_db_1", "VIEW_ONLY")
	grantForm.Set("access_db_2", "VIEW_EDIT")
	reqGrant := httptest.NewRequest(http.MethodPost, "/admin/users/access", strings.NewReader(grantForm.Encode()))
	reqGrant.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqGrant.Header.Set(apphttp.CSRFHeaderName, csrfCookie.Value)
	reqGrant.AddCookie(csrfCookie)
	for _, c := range recAdminLogin.Result().Cookies() {
		reqGrant.AddCookie(c)
	}
	recGrant := httptest.NewRecorder()
	srv.ServeHTTP(recGrant, reqGrant)
	if recGrant.Code != http.StatusOK || !strings.Contains(recGrant.Body.String(), dhiaID) || !strings.Contains(recGrant.Body.String(), "berhasil diperbarui") {
		t.Fatalf("seharusnya update hak akses user berhasil, status=%d, body=%s", recGrant.Code, recGrant.Body.String())
	}
}
