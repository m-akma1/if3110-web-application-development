package http

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Server) registerAPIRoutes() {
	// Auth
	s.mux.HandleFunc("GET /api/auth/me", s.handleAPIAuthMe)
	s.mux.HandleFunc("POST /api/auth/login", s.handleAPIAuthLogin)
	s.mux.HandleFunc("POST /api/auth/signup", s.handleAPIAuthSignup)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleAPIAuthLogout)

	// Viewer
	s.mux.HandleFunc("GET /api/viewer", s.handleAPIViewer)
	s.mux.HandleFunc("GET /api/viewer/table", s.handleAPIViewerTable)
	s.mux.HandleFunc("POST /api/viewer/query", s.handleAPIViewerQuery)

	// Migration
	s.mux.HandleFunc("GET /api/migration", s.handleAPIMigrationOverview)
	s.mux.HandleFunc("POST /api/migration/drafts", s.handleAPIMigrationCreateDraft)
	s.mux.HandleFunc("GET /api/migration/{id}", s.handleAPIMigrationDetail)
	s.mux.HandleFunc("POST /api/migration/{id}", s.handleAPIMigrationSaveDraft)
	s.mux.HandleFunc("DELETE /api/migration/{id}", s.handleAPIMigrationDeleteDraft)
	s.mux.HandleFunc("POST /api/migration/{id}/dry-run", s.handleAPIMigrationDryRun)
	s.mux.HandleFunc("POST /api/migration/{id}/execute", s.handleAPIMigrationExecute)
	s.mux.HandleFunc("POST /api/migration/{id}/duplicate", s.handleAPIMigrationDuplicate)

	// Admin
	s.mux.HandleFunc("GET /api/admin/overview", s.handleAPIAdminOverview)
	s.mux.HandleFunc("POST /api/admin/users", s.handleAPIAdminCreateUser)
	s.mux.HandleFunc("POST /api/admin/users/{id}/role", s.handleAPIAdminSetUserRole)
	s.mux.HandleFunc("POST /api/admin/users/{id}/active", s.handleAPIAdminSetUserActive)
	s.mux.HandleFunc("POST /api/admin/users/{id}/password", s.handleAPIAdminResetPassword)

	s.mux.HandleFunc("POST /api/admin/groups", s.handleAPIAdminCreateGroup)
	s.mux.HandleFunc("POST /api/admin/groups/{id}", s.handleAPIAdminRenameGroup)
	s.mux.HandleFunc("DELETE /api/admin/groups/{id}", s.handleAPIAdminDeleteGroup)
	s.mux.HandleFunc("POST /api/admin/groups/{id}/members", s.handleAPIAdminAddMember)
	s.mux.HandleFunc("DELETE /api/admin/groups/{id}/members/{userID}", s.handleAPIAdminRemoveMember)

	s.mux.HandleFunc("POST /api/admin/grants", s.handleAPIAdminSetGrant)

	s.mux.HandleFunc("POST /api/admin/databases", s.handleAPIAdminCreateTarget)
	s.mux.HandleFunc("POST /api/admin/databases/{id}", s.handleAPIAdminUpdateTarget)
	s.mux.HandleFunc("POST /api/admin/databases/{id}/test", s.handleAPIAdminTestTarget)
	s.mux.HandleFunc("DELETE /api/admin/databases/{id}", s.handleAPIAdminDeleteTarget)

	s.mux.HandleFunc("POST /api/admin/codes", s.handleAPIAdminCreateCode)
	s.mux.HandleFunc("POST /api/admin/codes/{code}/active", s.handleAPIAdminSetCodeActive)
}

func readJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}
