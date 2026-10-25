package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/views/pages"
)

// This file is the HTTP side of access control: identify the principal (authn), then ask
// rbac.Authorizer. Handlers never compare access levels or roles for database actions themselves.

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// layoutUser assembles the page view model from identity, group membership and effective grants.
func (s *Server) layoutUser(ctx context.Context, u *authn.User) (*domain.AuthLayoutUser, error) {
	layout := &domain.AuthLayoutUser{
		ID:              u.ID,
		Username:        u.Username,
		Email:           u.Email,
		Role:            u.Role,
		IsPlatformAdmin: u.Role == authn.RolePlatformAdmin,
		Groups:          []domain.GroupSummary{},
		Databases:       []domain.DatabaseAccessSummary{},
	}
	if s.groups != nil {
		gs, err := s.groups.ListForUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			layout.Groups = append(layout.Groups, domain.GroupSummary{ID: g.ID, Name: g.Name, IsSystem: g.IsSystem})
		}
	}
	if s.access != nil {
		dbs, err := s.access.AccessibleDatabases(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, db := range dbs {
			layout.Databases = append(layout.Databases, domain.DatabaseAccessSummary{
				DatabaseID:      db.DatabaseID,
				DatabaseName:    db.DatabaseName,
				DBMS:            "POSTGRESQL",
				EffectiveAccess: string(db.Level),
			})
		}
	}
	return layout, nil
}

// requireSession identifies the user or sends them to /login (HX-Redirect for HTMX requests).
func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) (*authn.User, *domain.AuthLayoutUser, bool) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		if isHTMX(r) {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
		return nil, nil, false
	}
	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		s.logger.Printf("[AUTHZ] gagal memuat profil user %s: %v", user.ID, err)
		http.Error(w, "Gagal memuat profil pengguna", http.StatusInternalServerError)
		return nil, nil, false
	}
	return user, layout, true
}

// requirePlatformAdmin allows only PLATFORM_ADMIN. This is a platform-management check,
// not a database access level: it grants nothing on target databases.
func (s *Server) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) (*authn.User, *domain.AuthLayoutUser, bool) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return nil, nil, false
	}
	if user.Role != authn.RolePlatformAdmin {
		http.Error(w, "403 Forbidden: Hanya Platform Admin yang diizinkan mengakses halaman ini", http.StatusForbidden)
		return nil, nil, false
	}
	return user, layout, true
}

// resolveDBID reads db_id from the query or form, defaulting to the first database the user can see.
func resolveDBID(r *http.Request, layout *domain.AuthLayoutUser) string {
	dbID := strings.TrimSpace(r.FormValue("db_id"))
	if dbID == "" && layout != nil && len(layout.Databases) > 0 {
		dbID = layout.Databases[0].DatabaseID
	}
	return dbID
}

// authorizeDB asks rbac whether the user may perform action on dbID.
// It returns rbac.ErrForbidden on denial; any other error is a storage failure and also denies.
func (s *Server) authorizeDB(ctx context.Context, u *authn.User, dbID string, action rbac.Action) (rbac.AccessLevel, error) {
	if s.access == nil || u == nil {
		return rbac.AccessNone, rbac.ErrForbidden
	}
	level, err := s.access.Authorize(ctx, u.ID, dbID, action)
	if err != nil && !errors.Is(err, rbac.ErrForbidden) {
		s.logger.Printf("[AUTHZ] gagal memeriksa akses user=%s db=%s action=%s: %v", u.ID, dbID, action, err)
	}
	return level, err
}

func writeFragmentStatus(w http.ResponseWriter, err error) {
	if errors.Is(err, rbac.ErrForbidden) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func accessDeniedMessage(err error) string {
	if errors.Is(err, rbac.ErrForbidden) {
		return "Akses ditolak: Anda tidak memiliki hak akses yang cukup pada database ini. Hubungi Platform Admin untuk ditambahkan ke group yang sesuai."
	}
	return "Gagal memeriksa hak akses. Silakan coba lagi."
}

// renderForbiddenPage renders a full page with 403 (or 500 when the check itself failed).
func (s *Server) renderForbiddenPage(w http.ResponseWriter, r *http.Request, layout *domain.AuthLayoutUser, csrfToken, activeTab string, err error) {
	writeFragmentStatus(w, err)
	_ = pages.Forbidden(layout, activeTab, accessDeniedMessage(err), csrfToken).Render(r.Context(), w)
}
