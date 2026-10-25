package http_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/app/internal/authn"
	apphttp "dbplatform/app/internal/http"
)

func TestSanitizeURL_Unit(t *testing.T) {
	rawURL, _ := url.Parse("/login?username=budi&password=Rahasia123&session_id=tokenAcak999")
	sanitized := apphttp.SanitizeURL(rawURL)

	if strings.Contains(sanitized, "Rahasia123") || strings.Contains(sanitized, "tokenAcak999") {
		t.Fatalf("password atau session id bocor pada URL yang disanitasi: %s", sanitized)
	}
	if !strings.Contains(sanitized, "password=[REDACTED]") || !strings.Contains(sanitized, "username=budi") {
		t.Fatalf("hanya parameter sensitif yang diganti [REDACTED], dapat: %s", sanitized)
	}
}

func TestCSRFGuard_BlocksPostWithoutToken_Unit(t *testing.T) {
	// The guard rejects before any handler runs, so no dependencies are needed.
	srv := apphttp.NewServer(&bytes.Buffer{}, apphttp.Deps{})
	form := url.Values{"identifier": {"admin"}, "password": {"SecretPassword123"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST tanpa token CSRF seharusnya 403, dapat: %d", rec.Code)
	}
}

// Login page -> login -> dashboard -> logout, through the htmx forms: cookie attributes,
// no secrets in responses or logs, and the old session is dead right after logout.
func TestLoginLogoutFlow_Integration(t *testing.T) {
	var logBuf bytes.Buffer
	srv := newTestServer(t, &logBuf)
	c := newClient(t, srv) // GET /login issues the CSRF cookie

	csrf := c.cookies[apphttp.CSRFCookieName]
	if !csrf.HttpOnly || !csrf.Secure || csrf.SameSite != http.SameSiteLaxMode || csrf.Path != "/" {
		t.Fatalf("atribut cookie CSRF tidak lengkap: %+v", csrf)
	}

	const password = "Rahasia123"
	login := c.post("/login", url.Values{"identifier": {"Alice"}, "password": {password}})
	if login.Header().Get("HX-Redirect") != "/dashboard" {
		t.Fatalf("login seharusnya mengirim HX-Redirect: /dashboard, dapat %q", login.Header().Get("HX-Redirect"))
	}
	sid := c.cookies[authn.SessionCookieName]
	if sid == nil || !sid.HttpOnly || !sid.Secure || sid.SameSite != http.SameSiteLaxMode || sid.Path != "/" {
		t.Fatalf("cookie sesi %s wajib HttpOnly, Secure, SameSite=Lax, Path=/: %+v", authn.SessionCookieName, sid)
	}
	oldSession := sid.Value

	dash := c.get("/dashboard")
	body := dash.Body.String()
	if dash.Code != http.StatusOK || !strings.Contains(body, "Database Viewer") || !strings.Contains(body, "alice") {
		t.Fatalf("dashboard seharusnya memuat user dan Database Viewer, status %d", dash.Code)
	}
	if !strings.Contains(body, "X-CSRF-Token") || !strings.Contains(body, csrf.Value) {
		t.Fatalf("halaman wajib menyertakan token CSRF agar form htmx (termasuk Logout) berfungsi")
	}

	for _, out := range []string{login.Body.String(), body, logBuf.String()} {
		if strings.Contains(out, password) || strings.Contains(out, oldSession) {
			t.Fatalf("password atau session id bocor ke response/log")
		}
	}

	logout := c.post("/logout", nil)
	if logout.Code != http.StatusOK || logout.Header().Get("HX-Redirect") != "/login" || c.cookies[authn.SessionCookieName] != nil {
		t.Fatalf("logout: status=%d redirect=%q", logout.Code, logout.Header().Get("HX-Redirect"))
	}

	// The old cookie is refused at once, even if the browser kept it.
	c.cookies[authn.SessionCookieName] = &http.Cookie{Name: authn.SessionCookieName, Value: oldSession}
	if rec := c.get("/dashboard"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("cookie lama setelah logout seharusnya diarahkan ke /login, dapat %d", rec.Code)
	}
}
