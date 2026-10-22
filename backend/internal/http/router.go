package http

import (
	"encoding/json"
	"net/http"
)

type Server struct {
	mux *http.ServeMux
}

func NewServer() *Server {
	s := &Server{
		mux: http.NewServeMux(),
	}
	s.registerRoutes()
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
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "db-management-platform-api",
	})
}
