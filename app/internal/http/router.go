package http

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/views/components"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

type Server struct {
	mux      *http.ServeMux
	handler  http.Handler
	logger   *log.Logger
	sessions *authn.SessionStore
}

// menyusun router beserta rantai middleware (Logger -> CSRF -> Mux)
func NewServer(logOutput io.Writer) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	logger := log.New(logOutput, "", log.LstdFlags)
	mux := http.NewServeMux()

	s := &Server{
		mux:      mux,
		logger:   logger,
		sessions: authn.NewSessionStore(),
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
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Static assets (Tailwind CSS, HTMX, Alpine.js)
	fs := http.FileServer(http.Dir("./static"))
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", fs))

	// GOTTH UI & HTMX Routes
	s.mux.HandleFunc("GET /", s.handleRoot)
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginHTMX)
	s.mux.HandleFunc("GET /dashboard", s.handleDashboardPage)
	s.mux.HandleFunc("GET /dashboard/select-db", s.handleDashboardPage)
	s.mux.HandleFunc("GET /dashboard/table", s.handleDashboardTable)
	s.mux.HandleFunc("POST /logout", s.handleLogoutHTMX)

	// JSON API Routes
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/csrf-token", s.handleGetCSRFToken)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/me", s.handleGetMe)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
}

// --- GOTTH UI & HTMX Handlers ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	token, err := GenerateCSRFToken()
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	templ.Handler(pages.Login(token, "")).ServeHTTP(w, r)
}

func (s *Server) handleLoginHTMX(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("password") != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginError("Password dilarang dikirim melalui URL").Render(r.Context(), w)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" || password == "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginError("Username dan password wajib diisi!").Render(r.Context(), w)
		return
	}

	opaqueSessionID, err := GenerateCSRFToken()
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginError("Gagal membuat sesi").Render(r.Context(), w)
		return
	}

	userProfile := authn.BuildUserProfile(username)
	s.sessions.Save(opaqueSessionID, userProfile)

	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    opaqueSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	sidCookie, err := r.Cookie(authn.SessionCookieName)
	if err != nil || sidCookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	user, ok := s.sessions.Get(sidCookie.Value)
	if !ok || user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	activeDBID := r.URL.Query().Get("db_id")
	if activeDBID == "" && len(user.Databases) > 0 {
		activeDBID = user.Databases[0].DatabaseID
	}

	dummyTables := []domain.TableSummary{
		{Name: "users", RowCount: 150, IsSelected: true},
		{Name: "groups", RowCount: 12, IsSelected: false},
		{Name: "migration_histories", RowCount: 45, IsSelected: false},
	}

	dummyData := domain.TableDataResult{
		TableName: "users",
		Columns:   []string{"id", "username", "role", "created_at"},
		Rows: [][]string{
			{"usr-101", "moreno", "PLATFORM_ADMIN", "2026-08-01 10:00"},
			{"usr-102", "dhia", "USER", "2026-08-02 11:30"},
			{"usr-103", "caca", "USER", "2026-08-03 14:15"},
		},
		TotalRows:   150,
		CurrentPage: 1,
		TotalPages:  15,
	}

	templ.Handler(pages.Dashboard(user, activeDBID, dummyTables, dummyData)).ServeHTTP(w, r)
}

func (s *Server) handleDashboardTable(w http.ResponseWriter, r *http.Request) {
	sidCookie, err := r.Cookie(authn.SessionCookieName)
	if err != nil || sidCookie.Value == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if _, ok := s.sessions.Get(sidCookie.Value); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	tableName := r.URL.Query().Get("name")
	if tableName == "" {
		tableName = "users"
	}

	dummyData := domain.TableDataResult{
		TableName: tableName,
		Columns:   []string{"id", "name", "status"},
		Rows: [][]string{
			{"1", "Sample Item A", "ACTIVE"},
			{"2", "Sample Item B", "PENDING"},
		},
		TotalRows:   2,
		CurrentPage: 1,
		TotalPages:  1,
	}

	_ = components.DataGrid(dummyData).Render(r.Context(), w)
}

func (s *Server) handleLogoutHTMX(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		s.sessions.Delete(sidCookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

// --- JSON API Handlers ---

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
		return
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

// Handler login JSON API
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

	opaqueSessionID, err := GenerateCSRFToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal membuat sesi")
		return
	}

	userProfile := authn.BuildUserProfile(req.Username)
	s.sessions.Save(opaqueSessionID, userProfile)

	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    opaqueSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "login berhasil",
		"username": req.Username,
		"user":     userProfile,
	})
}

// Handler mengambil profil & otorisasi user berdasarkan cookie sesi (__Host-sid)
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	sidCookie, err := r.Cookie(authn.SessionCookieName)
	if err != nil || sidCookie.Value == "" {
		writeJSONError(w, http.StatusUnauthorized, "Sesi tidak ditemukan atau belum login")
		return
	}

	user, ok := s.sessions.Get(sidCookie.Value)
	if !ok || user == nil {
		writeJSONError(w, http.StatusUnauthorized, "Sesi tidak valid atau telah berakhir")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// Handler logout untuk menghapus sesi aktif di server dan membersihkan cookie __Host-sid
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		s.sessions.Delete(sidCookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logout berhasil",
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
