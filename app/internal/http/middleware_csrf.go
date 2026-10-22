package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

const (
	CSRFCookieName = "__Host-csrf"
	CSRFHeaderName = "X-CSRF-Token"
)

// GenerateCSRFToken membuat token acak 32-byte
func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// CSRFGuardMiddleWare memvalidasi token CSRF
func CSRFGuardMiddleWare(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			csrfCookie, err := r.Cookie(CSRFCookieName)
			if err != nil || csrfCookie.Value == "" {
				writeJSONError(w, http.StatusForbidden, "CSRF cookie tidak ditemukan.")
				return
			}

			csrfHeader := r.Header.Get(CSRFHeaderName)
			if csrfHeader == "" {
				writeJSONError(w, http.StatusForbidden, "Header X-CSRF-Token wajib disertakan")
				return
			}

			if subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(csrfHeader)) != 1 {
				writeJSONError(w, http.StatusForbidden, "Token CSRF tidak valid")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
