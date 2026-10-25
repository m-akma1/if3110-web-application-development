package http

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dbplatform/app/internal/dbmanager"
)

func targetInputFromForm(r *http.Request) dbmanager.TargetInput {
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	return dbmanager.TargetInput{
		Name:     r.FormValue("name"),
		Host:     r.FormValue("host"),
		Port:     port,
		DBName:   r.FormValue("db_name"),
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
		SSLMode:  r.FormValue("ssl_mode"),
	}
}

// targetResultMessage reports the outcome of a connection test, which is not an error of the form itself.
func targetResultMessage(t *dbmanager.Target, action string) string {
	if t == nil {
		return ""
	}
	if t.Status == dbmanager.StatusConnected {
		return fmt.Sprintf("Database %s %s dan berhasil terhubung.", t.Name, action)
	}
	return fmt.Sprintf("Database %s %s, tetapi koneksi gagal: %s", t.Name, action, t.LastError)
}

func (s *Server) handleAdminCreateTarget(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	t, err := s.admin.CreateTarget(r.Context(), targetInputFromForm(r))
	s.renderAdminConsole(w, r, actor, targetResultMessage(t, "didaftarkan"), err)
}

func (s *Server) handleAdminUpdateTarget(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	t, err := s.admin.UpdateTarget(r.Context(), r.PathValue("id"), targetInputFromForm(r))
	s.renderAdminConsole(w, r, actor, targetResultMessage(t, "diperbarui"), err)
}

func (s *Server) handleAdminTestTarget(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	t, err := s.admin.TestTarget(r.Context(), r.PathValue("id"))
	s.renderAdminConsole(w, r, actor, targetResultMessage(t, "diuji"), err)
}

func (s *Server) handleAdminDeleteTarget(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.DeleteTarget(r.Context(), r.PathValue("id"))
	s.renderAdminConsole(w, r, actor, "Database dihapus dari platform beserta seluruh akses group-nya.", err)
}
