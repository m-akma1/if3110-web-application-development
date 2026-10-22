package http

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
)

type Server struct {
	mux     *http.ServeMux
	handler http.Handler
	logger  *log.Logger
}

// menyusun router beserta rantai middleware (Logger -> CSRF -> Mux)
func NewServer(logOutput io.Writer) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	logger := log.New(logOutput, "", log.LstdFlags)
	mux := http.NewServeMux()

	s := &Server{
		mux:    mux,
		logger: logger,
	}
	s.registerRoutes()

	var h http.Handler = s.mux
	// Request -> SanitizedLogger -> CSRFGuard -> Mux Handlers
	h = CSRFGuardMiddleWare(h)
	h = SanitizedLoggerMiddleware(s.logger)(h)

	s.handler = h
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Contoh pengaturan Cookie Session nantinya wajib menggunakan atribut berikut:
	// http.SetCookie(w, &http.Cookie{
	//     Name:     "__Host-sid",
	//     Value:    opaqueToken,
	//     Path:     "/",
	//     HttpOnly: true,
	//     Secure:   true,
	//     SameSite: http.SameSiteLaxMode,
	// })
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/csrf-token", s.handleGetCSRFToken)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "db-management-platform-api",
	})
}

// Handler Penerbitan Token CSRF untuk Browser / Frontend
func (S *Server) handleGetCSRFToken(w http.ResponseWriter, r *http.Request) {
	token, err := GenerateCSRFToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal membuat token CSRF")
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"csrf_token": token,
	})
}

// Handler login
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// tolak ketika pengguna mencoba mengirim password lewat URL query
	if r.URL.Query().Get("password") != "" {
		writeJSONError(w, http.StatusBadRequest, "Password dilarang dikirim melalui URL")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Payload JSON tidak valid")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "Username dan password wajib diisi")
		return
	}

	opaqueSessionID, _ := GenerateCSRFToken()

	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-sid",
		Value:    opaqueSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message":  "login berhasil",
		"username": req.Username,
	})
}

// helper menulis response JSON secara konsisten
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
