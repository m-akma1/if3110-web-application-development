package http

import (
	"net/http"

	"dbplatform/internal/authn"
)

// Cookies are HttpOnly, Secure and SameSite=Lax. The __Host- prefix of both names makes the
// browser also require Path=/ and no Domain, so a subdomain cannot overwrite them.

func secureCookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

func setSessionCookie(w http.ResponseWriter, opaqueSessionID string) {
	http.SetCookie(w, secureCookie(authn.SessionCookieName, opaqueSessionID, 0))
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, secureCookie(authn.SessionCookieName, "", -1))
}

// ensureCSRFToken returns the browser's CSRF token, issuing one if it has none. The pages put
// it in a meta tag and htmx sends it back as X-CSRF-Token (double-submit cookie, see CSRFGuardMiddleware).
func (s *Server) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		return c.Value, nil
	}
	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, secureCookie(CSRFCookieName, token, 0))
	return token, nil
}
