package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

// Admin overview. Creating groups, grants, invite codes and database connections
// is added by feat/m1-admin-console; this branch only lists them and changes roles.

func (s *Server) buildAdminUsersList(ctx context.Context) ([]domain.UserManagementItem, error) {
	users, err := s.sessions.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := s.groups.MemberGroupsByUser(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.UserManagementItem, 0, len(users))
	for _, u := range users {
		item := domain.UserManagementItem{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, IsActive: u.IsActive}
		for _, g := range memberships[u.ID] {
			item.Groups = append(item.Groups, domain.GroupSummary{ID: g.ID, Name: g.Name, IsSystem: g.IsSystem})
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	_, layout, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	users, err := s.buildAdminUsersList(r.Context())
	if err != nil {
		s.logger.Printf("[ADMIN] gagal memuat user: %v", err)
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}
	gs, err := s.groups.List(r.Context())
	if err != nil {
		s.logger.Printf("[ADMIN] gagal memuat group: %v", err)
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}
	codes, err := s.groups.ListInviteCodes(r.Context())
	if err != nil {
		s.logger.Printf("[ADMIN] gagal memuat kode undangan: %v", err)
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}

	groupItems := make([]domain.GroupItem, 0, len(gs))
	for _, g := range gs {
		groupItems = append(groupItems, domain.GroupItem{ID: g.ID, Name: g.Name, Description: g.Description, IsSystem: g.IsSystem, MemberCount: g.MemberCount})
	}
	codeItems := make([]domain.InviteCodeItem, 0, len(codes))
	for _, c := range codes {
		codeItems = append(codeItems, domain.InviteCodeItem{Code: c.Code, GroupName: c.GroupName, IsActive: c.IsActive, CreatedAt: c.CreatedAt})
	}

	templ.Handler(pages.Admin(layout, resolveDBID(r, layout), users, groupItems, codeItems, csrfToken)).ServeHTTP(w, r)
}

func (s *Server) handleAdminUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	role := strings.TrimSpace(r.FormValue("role"))

	feedback := ""
	err := s.sessions.UpdateUserRole(r.Context(), userID, role)
	switch {
	case errors.Is(err, authn.ErrInvalidRole):
		feedback = "Role tidak valid."
	case errors.Is(err, authn.ErrUserNotFound):
		feedback = "Pengguna tidak ditemukan."
	case err != nil:
		s.logger.Printf("[ADMIN] gagal mengubah role: %v", err)
		feedback = "Gagal mengubah role pengguna."
	default:
		feedback = fmt.Sprintf("Role pengguna berhasil diperbarui menjadi %s.", role)
	}

	users, listErr := s.buildAdminUsersList(r.Context())
	if listErr != nil {
		http.Error(w, "Gagal memuat data admin", http.StatusInternalServerError)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	_ = pages.AdminUsersTable(users, feedback, err == nil).Render(r.Context(), w)
}
