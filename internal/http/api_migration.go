package http

import (
	"net/http"
	"strings"

	"dbplatform/domain"
	"dbplatform/internal/authn"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"
)

func (s *Server) handleAPIMigrationOverview(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	dbID := resolveDBID(r, layout)
	if dbID == "" {
		writeJSON(w, http.StatusOK, domain.MigrationPageView{})
		return
	}
	if s.migrations == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Fitur migration belum dikonfigurasi")
		return
	}

	ov, err := s.migrations.Overview(r.Context(), user.ID, dbID)
	if err != nil {
		s.logMigrationError(user, "overview", err)
		writeJSONError(w, http.StatusForbidden, accessDeniedMessage(err))
		return
	}

	dbName := databaseName(layout, dbID)
	historyViews := toMigrationViews(ov.History)
	if r.URL.Query().Get("hide_rollback") == "true" {
		hiddenIDs := make(map[string]bool)
		for _, h := range historyViews {
			if strings.EqualFold(h.ExecutionType, "ROLLBACK") && strings.EqualFold(h.Status, "SUCCESS") {
				hiddenIDs[h.ID] = true
				if h.RollbackTargetID != "" {
					hiddenIDs[h.RollbackTargetID] = true
				}
			}
		}
		filtered := make([]domain.MigrationView, 0, len(historyViews))
		for _, h := range historyViews {
			if hiddenIDs[h.ID] {
				continue
			}
			filtered = append(filtered, h)
		}
		historyViews = filtered
	}

	page := domain.MigrationPageView{
		DatabaseID:   dbID,
		DatabaseName: dbName,
		AccessLevel:  string(ov.Level),
		CanEdit:      ov.CanEdit,
		Drafts:       toMigrationViews(ov.Drafts),
		History:      historyViews,
	}
	if draftID := r.URL.Query().Get("draft"); draftID != "" {
		for i := range page.Drafts {
			if page.Drafts[i].ID == draftID {
				page.Editing = &page.Drafts[i]
			}
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleAPIMigrationDetail(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	if s.migrations == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Fitur migration belum dikonfigurasi")
		return
	}
	m, level, err := s.migrations.Get(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.logMigrationError(user, "detail", err)
		writeJSONError(w, http.StatusForbidden, accessDeniedMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, domain.MigrationDetailView{
		Migration: toMigrationView(m),
		CanEdit:   rbac.IsAllowed(level, rbac.ActionCreateMigration),
	})
}

type apiMigrationDraftRequest struct {
	DatabaseID         string `json:"db_id"`
	Name               string `json:"name"`
	SQLScript          string `json:"sql_script"`
	SQLUp              string `json:"sql_up"`
	SQLDown            string `json:"sql_down"`
	IsIrreversible     bool   `json:"is_irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
}

func (s *Server) handleAPIMigrationCreateDraft(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var req apiMigrationDraftRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &req)
	} else {
		req.DatabaseID = r.FormValue("db_id")
		req.Name = r.FormValue("name")
		req.SQLScript = r.FormValue("sql_script")
		req.SQLUp = r.FormValue("sql_up")
		req.SQLDown = r.FormValue("sql_down")
		req.IsIrreversible = r.FormValue("is_irreversible") == "true"
		req.IrreversibleReason = r.FormValue("irreversible_reason")
	}

	dbID := req.DatabaseID
	if dbID == "" {
		dbID = resolveDBID(r, layout)
	}

	sqlScript := req.SQLScript
	if req.SQLUp != "" {
		sqlScript = req.SQLUp
	}
	m, err := s.migrations.CreateDraftFull(r.Context(), migration.NewDraft{
		DatabaseID:         dbID,
		Name:               req.Name,
		SQL:                sqlScript,
		SQLUp:              req.SQLUp,
		SQLDown:            req.SQLDown,
		IsIrreversible:     req.IsIrreversible,
		IrreversibleReason: req.IrreversibleReason,
		CreatedBy:          user.ID,
	})
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": toMigrationView(m)})
}

func (s *Server) handleAPIMigrationSaveDraft(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var req apiMigrationDraftRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &req)
	} else {
		req.Name = r.FormValue("name")
		req.SQLScript = r.FormValue("sql_script")
		req.SQLUp = r.FormValue("sql_up")
		req.SQLDown = r.FormValue("sql_down")
		req.IsIrreversible = r.FormValue("is_irreversible") == "true"
		req.IrreversibleReason = r.FormValue("irreversible_reason")
	}

	sqlScript := req.SQLScript
	if req.SQLUp != "" {
		sqlScript = req.SQLUp
	}
	m, err := s.migrations.SaveDraftFull(r.Context(), user.ID, r.PathValue("id"), migration.UpdateDraftInput{
		Name:               req.Name,
		SQL:                sqlScript,
		SQLUp:              req.SQLUp,
		SQLDown:            req.SQLDown,
		IsIrreversible:     req.IsIrreversible,
		IrreversibleReason: req.IrreversibleReason,
	})
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"draft":   toMigrationView(m),
		"message": "Draft disimpan.",
	})
}

func (s *Server) handleAPIMigrationDeleteDraft(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	_, err := s.migrations.DeleteDraft(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleAPIMigrationDryRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	var req apiMigrationDraftRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &req)
	} else {
		req.Name = r.FormValue("name")
		req.SQLScript = r.FormValue("sql_script")
	}

	// Only update draft if it is currently in an editable draft state (DRAFT or REJECTED).
	// Once approved, the approved script must be executed without modification.
	if req.SQLScript != "" {
		if cur, _, err := s.migrations.Get(r.Context(), user.ID, r.PathValue("id")); err == nil && cur.IsDraft() {
			_, _ = s.migrations.SaveDraft(r.Context(), user.ID, r.PathValue("id"), req.Name, req.SQLScript)
		}
	}

	_, res, err := s.migrations.DryRun(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, toDryRunView(res))
}

func (s *Server) handleAPIMigrationExecute(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	var req apiMigrationDraftRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &req)
	} else {
		req.Name = r.FormValue("name")
		req.SQLScript = r.FormValue("sql_script")
		req.SQLUp = r.FormValue("sql_up")
		req.SQLDown = r.FormValue("sql_down")
		req.IsIrreversible = r.FormValue("is_irreversible") == "true"
		req.IrreversibleReason = r.FormValue("irreversible_reason")
	}

	// Only update draft if it is currently in an editable draft state (DRAFT or REJECTED).
	// Once approved, the approved script must be executed without modification.
	if req.SQLScript != "" || req.SQLUp != "" {
		if cur, _, err := s.migrations.Get(r.Context(), user.ID, r.PathValue("id")); err == nil && cur.IsDraft() {
			sqlScript := req.SQLScript
			if req.SQLUp != "" {
				sqlScript = req.SQLUp
			}
			_, _ = s.migrations.SaveDraftFull(r.Context(), user.ID, r.PathValue("id"), migration.UpdateDraftInput{
				Name:               req.Name,
				SQL:                sqlScript,
				SQLUp:              req.SQLUp,
				SQLDown:            req.SQLDown,
				IsIrreversible:     req.IsIrreversible,
				IrreversibleReason: req.IrreversibleReason,
			})
		}
	}

	m, _, err := s.migrations.Execute(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": toMigrationView(m),
		"message":   "Migration berhasil dieksekusi.",
	})
}

func (s *Server) handleAPIMigrationRollback(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	m, res, err := s.migrations.Rollback(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.logMigrationError(user, "rollback", err)
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	if res == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"migration":         toMigrationView(m),
			"requires_approval": true,
			"message":           "Permintaan rollback diajukan untuk persetujuan Group Leader.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration":         toMigrationView(m),
		"requires_approval": false,
		"message":           "Rollback berhasil dieksekusi.",
		"result":            toDryRunView(res),
	})
}

func (s *Server) handleAPIMigrationForward(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	m, res, err := s.migrations.Forward(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.logMigrationError(user, "forward", err)
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	if res == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"migration":         toMigrationView(m),
			"requires_approval": true,
			"message":           "Permintaan forward diajukan untuk persetujuan Group Leader.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration":         toMigrationView(m),
		"requires_approval": false,
		"message":           "Forward berhasil dieksekusi.",
		"result":            toDryRunView(res),
	})
}

func (s *Server) handleAPIMigrationDuplicate(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	m, err := s.migrations.Duplicate(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": toMigrationView(m)})
}

func (s *Server) handleAPIMigrationPendingReviews(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	isPlatformAdmin := user.Role == authn.RolePlatformAdmin
	list, err := s.migrations.ListPendingReviews(r.Context(), user.ID, isPlatformAdmin)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat antrean review migrasi")
		return
	}
	views := toMigrationViews(list)
	writeJSON(w, http.StatusOK, map[string]any{
		"reviews": views,
	})
}

func (s *Server) handleAPIMigrationSubmitReview(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	m, err := s.migrations.SubmitReview(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": toMigrationView(m),
		"message":   "Migration berhasil diajukan untuk review Group Leader.",
	})
}

func (s *Server) handleAPIMigrationApprove(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Note = r.FormValue("note")
	}
	m, err := s.migrations.Approve(r.Context(), user.ID, r.PathValue("id"), strings.TrimSpace(body.Note))
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": toMigrationView(m),
		"message":   "Migration berhasil disetujui.",
	})
}

func (s *Server) handleAPIMigrationReject(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Reason = r.FormValue("reason")
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Alasan penolakan wajib diisi."})
		return
	}
	m, err := s.migrations.Reject(r.Context(), user.ID, r.PathValue("id"), reason)
	if err != nil {
		status, msg := migrationErrorResponse(err)
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migration": toMigrationView(m),
		"message":   "Migration berhasil ditolak.",
	})
}
