package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"dbplatform/app/internal/admin"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/migration"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

type Server struct {
	mux        *http.ServeMux
	handler    http.Handler
	logger     *log.Logger
	sessions   *authn.SessionStore
	groups     *groups.Repository
	access     *rbac.Authorizer
	admin      *admin.Service
	migrations *migration.Service
	dbRegistry *dbmanager.ConnectionRegistry
}

// Deps are the services the HTTP layer needs. All are backed by the platform PostgreSQL in production.
type Deps struct {
	Sessions *authn.SessionStore // identity and session lifecycle
	Groups   *groups.Repository  // groups, membership, invite codes
	Access   *rbac.Authorizer    // every database authorization decision
	Admin    *admin.Service      // platform admin console (users, groups, grants, codes, targets)
	// Registry holds live pools to target databases; shared by the viewer, the target manager
	// and the migration engine. Nil creates an empty one.
	Registry   *dbmanager.ConnectionRegistry
	Migrations *migration.Service // drafts, dry runs, execution and history of target migrations
}

// NewServer menyusun router beserta rantai middleware (Logger -> CSRF -> Mux)
func NewServer(logOutput io.Writer, deps Deps) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	logger := log.New(logOutput, "", log.LstdFlags)
	mux := http.NewServeMux()

	s := &Server{
		mux:        mux,
		logger:     logger,
		sessions:   deps.Sessions,
		groups:     deps.Groups,
		access:     deps.Access,
		admin:      deps.Admin,
		migrations: deps.Migrations,
		dbRegistry: deps.Registry,
	}
	if s.dbRegistry == nil {
		s.dbRegistry = dbmanager.NewConnectionRegistry()
	}
	s.registerRoutes()

	var h http.Handler = s.mux
	// Request -> SanitizedLogger -> CSRFGuard -> Mux Handlers
	h = CSRFGuardMiddleWare(h)
	h = SanitizedLoggerMiddleware(s.logger)(h)

	s.handler = h
	return s
}

// Sessions mengembalikan instance SessionStore milik server
func (s *Server) Sessions() *authn.SessionStore {
	return s.sessions
}

// RegisterDatabasePool mendaftarkan koneksi *sql.DB untuk database target tertentu
func (s *Server) RegisterDatabasePool(dbID string, db *sql.DB) {
	if s.dbRegistry != nil {
		s.dbRegistry.RegisterPool(dbID, db)
	}
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
	s.mux.HandleFunc("GET /auth/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /auth/login", s.handleLoginHTMX)
	s.mux.HandleFunc("GET /signup", s.handleSignupPage)
	s.mux.HandleFunc("POST /signup", s.handleSignupHTMX)
	s.mux.HandleFunc("GET /auth/signup", s.handleSignupPage)
	s.mux.HandleFunc("POST /auth/signup", s.handleSignupHTMX)
	s.registerViewerRoutes()
	s.registerMigrationRoutes()
	s.mux.HandleFunc("GET /admin", s.handleAdminPage)
	s.mux.HandleFunc("POST /admin/users", s.handleAdminCreateUser)
	s.mux.HandleFunc("POST /admin/users/{id}/role", s.handleAdminSetUserRole)
	s.mux.HandleFunc("POST /admin/users/{id}/active", s.handleAdminSetUserActive)
	s.mux.HandleFunc("POST /admin/users/{id}/password", s.handleAdminResetPassword)
	s.mux.HandleFunc("POST /admin/groups", s.handleAdminCreateGroup)
	s.mux.HandleFunc("POST /admin/groups/{id}", s.handleAdminRenameGroup)
	s.mux.HandleFunc("DELETE /admin/groups/{id}", s.handleAdminDeleteGroup)
	s.mux.HandleFunc("POST /admin/groups/{id}/members", s.handleAdminAddMember)
	s.mux.HandleFunc("DELETE /admin/groups/{id}/members/{userID}", s.handleAdminRemoveMember)
	s.mux.HandleFunc("POST /admin/grants", s.handleAdminSetGrant)
	s.mux.HandleFunc("POST /admin/databases", s.handleAdminCreateTarget)
	s.mux.HandleFunc("POST /admin/databases/{id}", s.handleAdminUpdateTarget)
	s.mux.HandleFunc("POST /admin/databases/{id}/test", s.handleAdminTestTarget)
	s.mux.HandleFunc("DELETE /admin/databases/{id}", s.handleAdminDeleteTarget)
	s.mux.HandleFunc("POST /admin/codes", s.handleAdminCreateCode)
	s.mux.HandleFunc("POST /admin/codes/{code}/active", s.handleAdminSetCodeActive)
	s.mux.HandleFunc("POST /logout", s.handleLogoutHTMX)

	// JSON API Routes
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/csrf-token", s.handleGetCSRFToken)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/me", s.handleGetMe)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
}

// validateRequestSession memeriksa idle timeout, absolute expiration, dan rotasi sesi otomatis pada setiap request.
// Hanya mengidentifikasi principal; hak akses diperiksa terpisah lewat rbac (lihat authz.go).
func (s *Server) validateRequestSession(w http.ResponseWriter, r *http.Request) (*authn.User, bool) {
	sidCookie, err := r.Cookie(authn.SessionCookieName)
	if err != nil || sidCookie.Value == "" {
		return nil, false
	}

	res, err := s.sessions.ValidateAndRefresh(r.Context(), sidCookie.Value)
	if err != nil || res == nil || res.User == nil {
		clearSessionCookie(w)
		return nil, false
	}

	// Jika Session Lifecycle Engine merotasi session identifier, perbarui cookie di sisi klien
	if res.Rotated && res.NewOpaqueID != "" {
		setSessionCookie(w, res.NewOpaqueID)
	}

	return res.User, true
}

func setSessionCookie(w http.ResponseWriter, opaqueSessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    opaqueSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- GOTTH UI & HTMX Handlers ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     CSRFCookieName,
			Value:    c.Value,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
		return c.Value, nil
	}

	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	token, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(pages.LoginForm(pages.AuthFormState{})).ServeHTTP(w, r)
		return
	}

	templ.Handler(pages.LoginPage(token, pages.AuthFormState{})).ServeHTTP(w, r)
}

func (s *Server) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	token, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(pages.SignupForm(pages.AuthFormState{})).ServeHTTP(w, r)
		return
	}

	templ.Handler(pages.SignupPage(token, pages.AuthFormState{})).ServeHTTP(w, r)
}

func (s *Server) handleLoginHTMX(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("password") != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(pages.AuthFormState{
			GeneralError: "Password dilarang dikirim melalui URL",
		}).Render(r.Context(), w)
		return
	}

	// Field "identifier" berisi username atau email; "email"/"username" diterima untuk kompatibilitas.
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("username"))
	}
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("email"))
	}
	password := r.FormValue("password")

	state := pages.AuthFormState{Identifier: identifier}
	if identifier == "" {
		state.IdentifierError = "Username atau email wajib diisi"
	}
	if password == "" {
		state.PasswordError = "Password wajib diisi"
	}
	if state.IdentifierError != "" || state.PasswordError != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(state).Render(r.Context(), w)
		return
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, _, err := s.sessions.Login(r.Context(), identifier, password, prevToken)
	if err != nil {
		state.GeneralError = "Username/email atau password tidak valid!"
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(state).Render(r.Context(), w)
		return
	}

	setSessionCookie(w, opaqueSessionID)

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSignupHTMX(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("password") != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(pages.AuthFormState{
			GeneralError: "Password dilarang dikirim melalui URL",
		}).Render(r.Context(), w)
		return
	}

	// Sign Up publik selalu membuat akun USER. Field account_type (jika dikirim) sengaja diabaikan.
	username := authn.NormalizeUsername(r.FormValue("username"))
	email := authn.NormalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	inviteCode := strings.TrimSpace(r.FormValue("invite_code"))

	state := pages.AuthFormState{
		Username:   username,
		Email:      email,
		InviteCode: inviteCode,
	}
	if username == "" {
		state.UsernameError = "Username wajib diisi"
	} else if err := authn.ValidateUsername(username); err != nil {
		state.UsernameError = "3-32 karakter: a-z, 0-9, titik, _ atau -"
	}
	if err := authn.ValidateEmail(email); err != nil {
		state.EmailError = "Format email tidak valid"
	}
	if password == "" {
		state.PasswordError = "Password wajib diisi"
	} else if err := authn.ValidatePassword(password); err != nil {
		state.PasswordError = "8-128 karakter"
	}
	// Kode undangan opsional: kosong berarti hanya menjadi anggota group Public (otomatis untuk semua user)
	var inviteGroup *groups.Group
	if inviteCode != "" {
		g, err := s.groups.ResolveInviteCode(r.Context(), inviteCode)
		if err != nil {
			state.CodeError = "Kode tim tidak valid"
		} else {
			inviteGroup = g
		}
	}

	if state.UsernameError != "" || state.EmailError != "" || state.PasswordError != "" || state.CodeError != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(state).Render(r.Context(), w)
		return
	}

	newUser, err := s.sessions.RegisterUser(r.Context(), authn.RegisterInput{
		Username: username,
		Email:    email,
		Password: password,
		Role:     authn.RoleUser,
	})
	switch {
	case errors.Is(err, authn.ErrUsernameTaken):
		state.UsernameError = "Username sudah terdaftar"
	case errors.Is(err, authn.ErrEmailTaken):
		state.EmailError = "Email sudah terdaftar"
	case err != nil:
		state.GeneralError = "Gagal mendaftarkan akun, silakan coba lagi."
	}
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(state).Render(r.Context(), w)
		return
	}

	if inviteGroup != nil {
		if err := s.groups.AddMember(r.Context(), inviteGroup.ID, newUser.ID); err != nil {
			// Akun tetap dibuat (anggota Public); admin dapat menambahkan group secara manual
			s.logger.Printf("[SIGNUP] gagal menambahkan user ke group %s: %v", inviteGroup.ID, err)
		}
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, _, err := s.sessions.Login(r.Context(), username, password, prevToken)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(pages.AuthFormState{
			Identifier:   username,
			GeneralError: "Akun berhasil dibuat, silakan login.",
		}).Render(r.Context(), w)
		return
	}

	setSessionCookie(w, opaqueSessionID)

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogoutHTMX(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

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

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, user, err := s.sessions.Login(r.Context(), req.Username, req.Password, prevToken)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Username atau password tidak valid")
		return
	}

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil pengguna")
		return
	}

	setSessionCookie(w, opaqueSessionID)

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "login berhasil",
		"username": user.Username,
		"user":     layout,
	})
}

// Handler mengambil profil & otorisasi user berdasarkan cookie sesi (__Host-sid)
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "Sesi tidak valid atau telah berakhir")
		return
	}

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil pengguna")
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

// Handler logout untuk menghapus sesi aktif di server dan membersihkan cookie __Host-sid
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

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
