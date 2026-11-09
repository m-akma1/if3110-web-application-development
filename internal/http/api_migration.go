package http

import (
	"errors"
	"fmt"
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
	page := domain.MigrationPageView{
		DatabaseID:   dbID,
		DatabaseName: dbName,
		AccessLevel:  string(ov.Level),
		CanEdit:      ov.CanEdit,
		Drafts:       toMigrationViews(ov.Drafts),
		History:      toMigrationViews(ov.History),
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
	DatabaseID string `json:"db_id"`
	Name       string `json:"name"`
	SQLScript  string `json:"sql_script"`
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
	}

	dbID := req.DatabaseID
	if dbID == "" {
		dbID = resolveDBID(r, layout)
	}

	m, err := s.migrations.CreateDraft(r.Context(), user.ID, dbID, req.Name, req.SQLScript)
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
	}

	m, err := s.migrations.SaveDraft(r.Context(), user.ID, r.PathValue("id"), req.Name, req.SQLScript)
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
	}

	// Only update draft if it is currently in an editable draft state (DRAFT or REJECTED).
	// Once approved, the approved script must be executed without modification.
	if req.SQLScript != "" {
		if cur, _, err := s.migrations.Get(r.Context(), user.ID, r.PathValue("id")); err == nil && cur.IsDraft() {
			_, _ = s.migrations.SaveDraft(r.Context(), user.ID, r.PathValue("id"), req.Name, req.SQLScript)
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

func migrationErrorResponse(err error) (int, string) {
	var rejected *migration.RejectedError
	switch {
	case errors.Is(err, rbac.ErrForbidden):
		return http.StatusForbidden, accessDeniedMessage(err)
	case errors.As(err, &rejected):
		return http.StatusUnprocessableEntity, "Script ditolak sebelum dijalankan: " + rejected.Error()
	case errors.Is(err, migration.ErrTargetUnavailable):
		return http.StatusUnprocessableEntity, "Database target sedang tidak terhubung. Minta Platform Admin memeriksa koneksinya, lalu coba lagi."
	case errors.Is(err, migration.ErrNotApproved),
		errors.Is(err, migration.ErrNotPendingReview),
		errors.Is(err, migration.ErrNotLeader),
		errors.Is(err, migration.ErrAlreadyReviewed),
		errors.Is(err, migration.ErrInvalidName),
		errors.Is(err, migration.ErrEmptyScript),
		errors.Is(err, migration.ErrScriptTooLarge),
		errors.Is(err, migration.ErrNotDraft),
		errors.Is(err, migration.ErrTargetBusy):
		return http.StatusUnprocessableEntity, err.Error()
	default:
		return http.StatusInternalServerError, "Terjadi kesalahan pada server. Silakan coba lagi."
	}
}

func (s *Server) logMigrationError(user *authn.User, op string, err error) {
	if errors.Is(err, rbac.ErrForbidden) {
		return
	}
	s.logger.Printf("[MIGRATION] %s gagal user=%s: %v", op, user.ID, err)
}

func toMigrationView(m *migration.Migration) domain.MigrationView {
	approvals := make([]domain.MigrationApproval, 0, len(m.Approvals))
	for _, a := range m.Approvals {
		approvals = append(approvals, domain.MigrationApproval{
			ID:              a.ID,
			MigrationID:     a.MigrationID,
			GroupID:         a.GroupID,
			GroupName:       a.GroupName,
			LeaderID:        a.LeaderID,
			LeaderName:      a.LeaderName,
			Status:          a.Status,
			ReviewedAt:      a.ReviewedAt,
			Note:            a.Note,
			RejectionReason: a.RejectionReason,
		})
	}
	return domain.MigrationView{
		ID:              m.ID,
		DatabaseID:      m.DatabaseID,
		DatabaseName:    m.DatabaseName,
		Version:         m.Version,
		Name:            m.Name,
		SQL:             m.SQL,
		Status:          string(m.Status),
		ReviewRequired:  m.ReviewRequired,
		CanApprove:      m.CanApprove,
		RejectionReason: m.RejectionReason,
		Approvals:       approvals,
		CreatedByName:   orDash(m.CreatedByName),
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		ExecutedByName:  orDash(m.ExecutedByName),
		ExecutedAt:      m.ExecutedAt,
		DurationMs:      m.DurationMs,
		Error:           m.Error,
	}
}

func toMigrationViews(list []*migration.Migration) []domain.MigrationView {
	out := make([]domain.MigrationView, 0, len(list))
	for _, m := range list {
		out = append(out, toMigrationView(m))
	}
	return out
}

func toDryRunView(res *migration.RunResult) domain.DryRunView {
	v := domain.DryRunView{OK: res.OK, Statements: res.Statements, DurationMs: ms(res.Duration.Seconds())}
	if res.Err != nil {
		v.Error = res.Err.String()
	}
	for _, st := range res.Steps {
		sv := domain.DryRunStepView{Index: st.Index, SQL: st.SQL, Kind: string(st.Kind), DurationMs: ms(st.Duration.Seconds())}
		if st.Plan != nil {
			sv.PlanningMs, sv.ExecutionMs = st.Plan.PlanningTime, st.Plan.ExecutionTime
			for _, row := range st.Plan.Rows() {
				sv.Nodes = append(sv.Nodes, toPlanNodeView(row))
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

func toPlanNodeView(row migration.PlanRow) domain.PlanNodeView {
	n := row.Node
	title := n.NodeType
	if n.Operation != "" && n.NodeType == "ModifyTable" {
		title = n.Operation
	}
	if n.Strategy != "" && n.NodeType == "Aggregate" {
		title = n.Strategy + " " + title
	}
	if n.IndexName != "" {
		title += " using " + n.IndexName
	}
	if n.RelationName != "" {
		rel := n.RelationName
		if n.Schema != "" {
			rel = n.Schema + "." + rel
		}
		if n.Alias != "" && n.Alias != n.RelationName {
			rel += " " + n.Alias
		}
		title += " on " + rel
	}
	var conds []string
	for _, c := range []struct{ label, value string }{
		{"Index Cond", n.IndexCond}, {"Recheck Cond", n.RecheckCond}, {"Hash Cond", n.HashCond},
		{"Merge Cond", n.MergeCond}, {"Join Filter", n.JoinFilter}, {"Filter", n.Filter},
	} {
		if c.value != "" {
			conds = append(conds, fmt.Sprintf("%s: %s", c.label, c.value))
		}
	}
	return domain.PlanNodeView{
		Depth:         row.Depth,
		Title:         title,
		NodeType:      n.NodeType,
		JoinType:      n.JoinType,
		EstRows:       n.PlanRows,
		ActualRows:    n.ActualRows,
		Loops:         n.ActualLoops,
		StartupCost:   n.StartupCost,
		TotalCost:     n.TotalCost,
		ActualMs:      n.ActualTotalTime,
		Conditions:    conds,
		RowsRemoved:   n.RowsRemovedByFilter,
		SharedHit:     n.SharedHitBlocks,
		SharedRead:    n.SharedReadBlocks,
		NeverExecuted: n.NeverExecuted(),
		Misestimated:  n.Misestimated(),
	}
}

func ms(seconds float64) float64 { return seconds * 1000 }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
