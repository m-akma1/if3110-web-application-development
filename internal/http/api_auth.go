package http

import (
	"errors"
	"net/http"
	"strings"

	"dbplatform/domain"
	"dbplatform/internal/authn"
	"dbplatform/internal/groups"
)

type apiAuthUser struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

type apiAuthResponse struct {
	User      *apiAuthUser                   `json:"user"`
	Groups    []domain.GroupSummary          `json:"groups"`
	Databases []domain.DatabaseAccessSummary `json:"databases"`
	CSRFToken string                         `json:"csrf_token"`
}

func (s *Server) handleAPIAuthMe(w http.ResponseWriter, r *http.Request) {
	token, _ := s.ensureCSRFToken(w, r)
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":      "unauthorized",
			"csrf_token": token,
		})
		return
	}

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil pengguna")
		return
	}

	writeJSON(w, http.StatusOK, apiAuthResponse{
		User: &apiAuthUser{
			ID:              user.ID,
			Username:        user.Username,
			Email:           user.Email,
			Role:            user.Role,
			IsPlatformAdmin: user.Role == authn.RolePlatformAdmin,
		},
		Groups:    layout.Groups,
		Databases: layout.Databases,
		CSRFToken: token,
	})
}

type apiLoginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

func (s *Server) handleAPIAuthLogin(w http.ResponseWriter, r *http.Request) {
	token, _ := s.ensureCSRFToken(w, r)

	var req apiLoginRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := readJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
	} else {
		req.Identifier = r.FormValue("identifier")
		req.Password = r.FormValue("password")
	}

	req.Identifier = strings.TrimSpace(req.Identifier)
	fieldErrors := map[string]string{}
	if req.Identifier == "" {
		fieldErrors["identifier_error"] = "Username atau email wajib diisi"
	}
	if req.Password == "" {
		fieldErrors["password_error"] = "Password wajib diisi"
	}
	if len(fieldErrors) > 0 {
		fieldErrors["error"] = "Validasi gagal"
		fieldErrors["csrf_token"] = token
		writeJSON(w, http.StatusUnprocessableEntity, fieldErrors)
		return
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, user, err := s.sessions.Login(r.Context(), req.Identifier, req.Password, prevToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":      "Username/email atau password tidak valid!",
			"csrf_token": token,
		})
		return
	}

	setSessionCookie(w, opaqueSessionID)

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil")
		return
	}

	writeJSON(w, http.StatusOK, apiAuthResponse{
		User: &apiAuthUser{
			ID:              user.ID,
			Username:        user.Username,
			Email:           user.Email,
			Role:            user.Role,
			IsPlatformAdmin: user.Role == authn.RolePlatformAdmin,
		},
		Groups:    layout.Groups,
		Databases: layout.Databases,
		CSRFToken: token,
	})
}

type apiSignupRequest struct {
	Username   string `json:"username"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"`
}

func (s *Server) handleAPIAuthSignup(w http.ResponseWriter, r *http.Request) {
	token, _ := s.ensureCSRFToken(w, r)

	var req apiSignupRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := readJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
	} else {
		req.Username = r.FormValue("username")
		req.Email = r.FormValue("email")
		req.Password = r.FormValue("password")
		req.InviteCode = r.FormValue("invite_code")
	}

	username := authn.NormalizeUsername(req.Username)
	email := authn.NormalizeEmail(req.Email)
	password := req.Password
	inviteCode := strings.TrimSpace(req.InviteCode)

	fieldErrors := map[string]string{}
	if username == "" {
		fieldErrors["username_error"] = "Username wajib diisi"
	} else if err := authn.ValidateUsername(username); err != nil {
		fieldErrors["username_error"] = "3-32 karakter: a-z, 0-9, titik, _ atau -"
	}
	if err := authn.ValidateEmail(email); err != nil {
		fieldErrors["email_error"] = "Format email tidak valid"
	}
	if password == "" {
		fieldErrors["password_error"] = "Password wajib diisi"
	} else if err := authn.ValidatePassword(password); err != nil {
		fieldErrors["password_error"] = "8-128 karakter"
	}

	var inviteGroup *groups.Group
	if inviteCode != "" {
		g, err := s.groups.ResolveInviteCode(r.Context(), inviteCode)
		if err != nil {
			fieldErrors["code_error"] = "Kode tim tidak valid"
		} else {
			inviteGroup = g
		}
	}

	if len(fieldErrors) > 0 {
		fieldErrors["error"] = "Validasi formulir gagal"
		fieldErrors["csrf_token"] = token
		writeJSON(w, http.StatusUnprocessableEntity, fieldErrors)
		return
	}

	newUser, err := s.sessions.RegisterUser(r.Context(), authn.RegisterInput{
		Username: username,
		Email:    email,
		Password: password,
		Role:     authn.RoleUser,
	})
	if err != nil {
		switch {
		case errors.Is(err, authn.ErrUsernameTaken):
			fieldErrors["username_error"] = "Username sudah terdaftar"
		case errors.Is(err, authn.ErrEmailTaken):
			fieldErrors["email_error"] = "Email sudah terdaftar"
		default:
			fieldErrors["error"] = "Gagal mendaftarkan akun, silakan coba lagi."
		}
		fieldErrors["csrf_token"] = token
		writeJSON(w, http.StatusUnprocessableEntity, fieldErrors)
		return
	}

	if inviteGroup != nil {
		_ = s.groups.AddMember(r.Context(), inviteGroup.ID, newUser.ID)
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, _, err := s.sessions.Login(r.Context(), username, password, prevToken)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"message":    "Akun berhasil dibuat, silakan login.",
			"csrf_token": token,
		})
		return
	}

	setSessionCookie(w, opaqueSessionID)

	layout, _ := s.layoutUser(r.Context(), newUser)
	writeJSON(w, http.StatusOK, apiAuthResponse{
		User: &apiAuthUser{
			ID:              newUser.ID,
			Username:        newUser.Username,
			Email:           newUser.Email,
			Role:            newUser.Role,
			IsPlatformAdmin: false,
		},
		Groups:    layout.Groups,
		Databases: layout.Databases,
		CSRFToken: token,
	})
}

func (s *Server) handleAPIAuthLogout(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
