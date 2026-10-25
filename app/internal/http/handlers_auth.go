package http

import (
	"errors"
	"net/http"
	"strings"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/groups"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

// Login, signup and logout. Forms are posted by htmx; success answers with HX-Redirect.
func (s *Server) registerAuthRoutes() {
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginHTMX)
	s.mux.HandleFunc("GET /signup", s.handleSignupPage)
	s.mux.HandleFunc("POST /signup", s.handleSignupHTMX)
	s.mux.HandleFunc("POST /logout", s.handleLogout)
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

	// "identifier" berisi username atau email
	identifier := strings.TrimSpace(r.FormValue("identifier"))
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

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}
