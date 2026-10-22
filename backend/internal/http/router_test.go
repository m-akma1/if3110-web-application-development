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

	apphttp "dbplatform/backend/internal/http"
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
	srv := apphttp.NewServer(&logBuf)

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
	srv := apphttp.NewServer(&logBuf)

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
