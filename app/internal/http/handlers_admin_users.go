package http

import (
	"fmt"
	"net/http"
	"strings"

	"dbplatform/app/internal/authn"
)

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Form tidak valid", http.StatusBadRequest)
		return
	}
	in := authn.RegisterInput{
		Username: r.PostFormValue("username"),
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
		Role:     strings.TrimSpace(r.PostFormValue("role")),
	}
	u, err := s.admin.CreateUser(r.Context(), in, r.PostForm["group_ids"])
	msg := ""
	if u != nil {
		msg = fmt.Sprintf("User %s berhasil dibuat.", u.Username)
	}
	s.renderAdminConsole(w, r, actor, msg, err)
}

func (s *Server) handleAdminSetUserRole(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	role := strings.TrimSpace(r.FormValue("role"))
	err := s.admin.SetUserRole(r.Context(), actor.ID, r.PathValue("id"), role)
	s.renderAdminConsole(w, r, actor, fmt.Sprintf("Role diperbarui menjadi %s.", role), err)
}

func (s *Server) handleAdminSetUserActive(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	active := r.FormValue("active") == "true"
	err := s.admin.SetUserActive(r.Context(), actor.ID, r.PathValue("id"), active)
	msg := "Akun dinonaktifkan dan seluruh sesinya dicabut."
	if active {
		msg = "Akun diaktifkan kembali."
	}
	s.renderAdminConsole(w, r, actor, msg, err)
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.ResetPassword(r.Context(), r.PathValue("id"), r.FormValue("password"))
	s.renderAdminConsole(w, r, actor, "Password direset. User harus login ulang dengan password baru.", err)
}
