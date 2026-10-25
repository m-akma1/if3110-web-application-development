package http

import (
	"errors"
	"net/http"

	"dbplatform/domain"
	"dbplatform/internal/admin"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	"dbplatform/internal/rbac"
	"dbplatform/views/pages"

	"github.com/a-h/templ"
)

// Admin console. Every admin action re-renders the whole console (#admin-console), so all tabs
// stay consistent; the active tab lives in Alpine state outside the swapped element.

func (s *Server) registerAdminRoutes() {
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
}

var adminTabs = map[string]bool{"users": true, "groups": true, "databases": true, "codes": true}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}
	ov, err := s.admin.Overview(r.Context())
	if err != nil {
		s.logger.Printf("[ADMIN] gagal memuat data: %v", err)
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}
	tab := r.URL.Query().Get("tab")
	if !adminTabs[tab] {
		tab = "users"
	}
	templ.Handler(pages.Admin(layout, resolveDBID(r, layout), tab, user.ID, *ov, csrfToken)).ServeHTTP(w, r)
}

// renderAdminConsole answers an admin action: 200 with a success flash, or 422/500 with an error flash.
func (s *Server) renderAdminConsole(w http.ResponseWriter, r *http.Request, actor *authn.User, successMsg string, actionErr error) {
	flash := &domain.Flash{Success: true, Message: successMsg}
	status := http.StatusOK
	if actionErr != nil {
		msg, code := adminErrorMessage(actionErr)
		if code == http.StatusInternalServerError {
			s.logger.Printf("[ADMIN] aksi gagal: %v", actionErr)
		}
		flash = &domain.Flash{Success: false, Message: msg}
		status = code
	}
	ov, err := s.admin.Overview(r.Context())
	if err != nil {
		s.logger.Printf("[ADMIN] gagal memuat data: %v", err)
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_ = pages.AdminConsole(actor.ID, *ov, flash).Render(r.Context(), w)
}

// adminErrorMessage maps known domain errors to a user-facing message and 422; anything else is a 500.
func adminErrorMessage(err error) (string, int) {
	var ve *dbmanager.ValidationError
	if errors.As(err, &ve) {
		return "Data koneksi tidak valid: " + ve.Message, http.StatusUnprocessableEntity
	}
	known := []error{
		admin.ErrSelfLockout, admin.ErrInvalidLevel,
		authn.ErrInvalidUsername, authn.ErrInvalidEmail, authn.ErrWeakPassword, authn.ErrUsernameTaken,
		authn.ErrEmailTaken, authn.ErrInvalidRole, authn.ErrUserNotFound,
		groups.ErrGroupNotFound, groups.ErrGroupNameTaken, groups.ErrInvalidGroupName, groups.ErrInvalidInviteCode,
		groups.ErrInviteCodeTaken, groups.ErrSystemGroup,
		rbac.ErrUnknownGrantTarget,
		dbmanager.ErrTargetNotFound, dbmanager.ErrTargetNameTaken,
	}
	for _, k := range known {
		if errors.Is(err, k) {
			return k.Error(), http.StatusUnprocessableEntity
		}
	}
	return "Terjadi kesalahan pada server. Silakan coba lagi.", http.StatusInternalServerError
}
