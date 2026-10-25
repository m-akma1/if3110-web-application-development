package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/migration"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/views/components"
	"dbplatform/app/views/pages"
)

// Migration feature routes. Access is decided by migration.Service (through rbac) on every
// request; the templates only hide what the user cannot do.
func (s *Server) registerMigrationRoutes() {
	s.mux.HandleFunc("GET /migration", s.handleMigrationPage)
	s.mux.HandleFunc("POST /migration/drafts", s.handleMigrationCreate)
	s.mux.HandleFunc("GET /migration/{id}", s.handleMigrationDetail)
	s.mux.HandleFunc("POST /migration/{id}", s.handleMigrationSave)
	s.mux.HandleFunc("POST /migration/{id}/delete", s.handleMigrationDelete)
	s.mux.HandleFunc("POST /migration/{id}/dry-run", s.handleMigrationDryRun)
	s.mux.HandleFunc("POST /migration/{id}/execute", s.handleMigrationExecute)
	s.mux.HandleFunc("POST /migration/{id}/duplicate", s.handleMigrationDuplicate)
}

// maxMigrationBody leaves room for the other form fields next to the largest allowed script.
const maxMigrationBody = migration.MaxScriptLength + 64<<10

func (s *Server) handleMigrationPage(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	dbID := resolveDBID(r, layout)
	if dbID == "" {
		_ = pages.Migration(layout, domain.MigrationPageView{}, csrfToken).Render(r.Context(), w)
		return
	}
	if s.migrations == nil {
		http.Error(w, "Fitur migration belum dikonfigurasi", http.StatusServiceUnavailable)
		return
	}
	ov, err := s.migrations.Overview(r.Context(), user.ID, dbID)
	if err != nil {
		s.logMigrationError(user, "overview", err)
		s.renderForbiddenPage(w, r, layout, csrfToken, "migration", err)
		return
	}

	page := domain.MigrationPageView{
		DatabaseID:  dbID,
		AccessLevel: string(ov.Level),
		CanEdit:     ov.CanEdit,
		Drafts:      toMigrationViews(ov.Drafts),
		History:     toMigrationViews(ov.History),
	}
	for _, db := range layout.Databases {
		if db.DatabaseID == dbID {
			page.DatabaseName = db.DatabaseName
		}
	}
	if draftID := r.URL.Query().Get("draft"); draftID != "" {
		for i := range page.Drafts {
			if page.Drafts[i].ID == draftID {
				page.Editing = &page.Drafts[i]
			}
		}
	}
	_ = pages.Migration(layout, page, csrfToken).Render(r.Context(), w)
}

func (s *Server) handleMigrationDetail(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}
	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}
	if s.migrations == nil {
		http.Error(w, "Fitur migration belum dikonfigurasi", http.StatusServiceUnavailable)
		return
	}
	m, level, err := s.migrations.Get(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.logMigrationError(user, "detail", err)
		s.renderForbiddenPage(w, r, layout, csrfToken, "migration", err)
		return
	}
	_ = pages.MigrationDetail(layout, domain.MigrationDetailView{
		Migration: toMigrationView(m),
		CanEdit:   rbac.IsAllowed(level, rbac.ActionCreateMigration),
	}, csrfToken).Render(r.Context(), w)
}

func (s *Server) handleMigrationCreate(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.migrationRequest(w, r)
	if !ok {
		return
	}
	dbID := resolveDBID(r, layout)
	m, err := s.migrations.CreateDraft(r.Context(), user.ID, dbID, r.PostFormValue("name"), r.PostFormValue("sql_script"))
	if err != nil {
		s.writeMigrationError(w, r, user, "create", err)
		return
	}
	redirect(w, r, migrationURL(m.DatabaseID, m.ID))
}

func (s *Server) handleMigrationSave(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.migrationRequest(w, r)
	if !ok {
		return
	}
	if _, err := s.migrations.SaveDraft(r.Context(), user.ID, r.PathValue("id"), r.PostFormValue("name"), r.PostFormValue("sql_script")); err != nil {
		s.writeMigrationError(w, r, user, "save", err)
		return
	}
	_ = pages.MigrationFeedback(true, "Draft disimpan.").Render(r.Context(), w)
}

func (s *Server) handleMigrationDelete(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.migrationRequest(w, r)
	if !ok {
		return
	}
	m, err := s.migrations.DeleteDraft(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeMigrationError(w, r, user, "delete", err)
		return
	}
	redirect(w, r, migrationURL(m.DatabaseID, ""))
}

// handleMigrationDryRun saves the editor content (when sent) and dry-runs the draft.
func (s *Server) handleMigrationDryRun(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.migrationRequest(w, r)
	if !ok || !s.saveIfSent(w, r, user) {
		return
	}
	_, res, err := s.migrations.DryRun(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeMigrationError(w, r, user, "dry-run", err)
		return
	}
	_ = components.DryRunReport(toDryRunView(res)).Render(r.Context(), w)
}

// handleMigrationExecute saves the editor content (when sent), executes the draft and opens its
// detail page. A failed script is recorded as FAILED and also lands on the detail page.
func (s *Server) handleMigrationExecute(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.migrationRequest(w, r)
	if !ok || !s.saveIfSent(w, r, user) {
		return
	}
	m, _, err := s.migrations.Execute(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeMigrationError(w, r, user, "execute", err)
		return
	}
	s.logger.Printf("[MIGRATION] user=%s db=%s migration=%s status=%s version=%d", user.ID, m.DatabaseID, m.ID, m.Status, m.Version)
	redirect(w, r, "/migration/"+url.PathEscape(m.ID))
}

func (s *Server) handleMigrationDuplicate(w http.ResponseWriter, r *http.Request) {
	user, _, ok := s.migrationRequest(w, r)
	if !ok {
		return
	}
	m, err := s.migrations.Duplicate(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeMigrationError(w, r, user, "duplicate", err)
		return
	}
	redirect(w, r, migrationURL(m.DatabaseID, m.ID))
}

// migrationRequest identifies the user and parses a size-limited form for a migration POST.
func (s *Server) migrationRequest(w http.ResponseWriter, r *http.Request) (*authn.User, *domain.AuthLayoutUser, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMigrationBody)
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return nil, nil, false
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = pages.MigrationFeedback(false, migration.ErrScriptTooLarge.Error()).Render(r.Context(), w)
		return nil, nil, false
	}
	if s.migrations == nil {
		http.Error(w, "Fitur migration belum dikonfigurasi", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	return user, layout, true
}

// saveIfSent saves the draft first when the request carries the editor's fields, so dry runs
// and executions always use what the user sees in the editor.
func (s *Server) saveIfSent(w http.ResponseWriter, r *http.Request, user *authn.User) bool {
	if _, sent := r.PostForm["sql_script"]; !sent {
		return true
	}
	if _, err := s.migrations.SaveDraft(r.Context(), user.ID, r.PathValue("id"), r.PostFormValue("name"), r.PostFormValue("sql_script")); err != nil {
		s.writeMigrationError(w, r, user, "save", err)
		return false
	}
	return true
}

// writeMigrationError renders a feedback fragment. Denials are 403, problems the user can fix
// are 422 (both are swapped into the page by htmx), anything else is 500.
func (s *Server) writeMigrationError(w http.ResponseWriter, r *http.Request, user *authn.User, op string, err error) {
	status, msg := migrationErrorResponse(err)
	if status == http.StatusInternalServerError {
		s.logMigrationError(user, op, err)
	}
	w.WriteHeader(status)
	_ = pages.MigrationFeedback(false, msg).Render(r.Context(), w)
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
	case errors.Is(err, migration.ErrInvalidName),
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

// redirect sends HTMX requests to url with HX-Redirect and others with 303.
func redirect(w http.ResponseWriter, r *http.Request, url string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func migrationURL(dbID, draftID string) string {
	q := url.Values{"db_id": {dbID}}
	if draftID != "" {
		q.Set("draft", draftID)
	}
	return "/migration?" + q.Encode()
}

func toMigrationView(m *migration.Migration) domain.MigrationView {
	return domain.MigrationView{
		ID:             m.ID,
		DatabaseID:     m.DatabaseID,
		DatabaseName:   m.DatabaseName,
		Version:        m.Version,
		Name:           m.Name,
		SQL:            m.SQL,
		Status:         string(m.Status),
		CreatedByName:  orDash(m.CreatedByName),
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		ExecutedByName: orDash(m.ExecutedByName),
		ExecutedAt:     m.ExecutedAt,
		DurationMs:     m.DurationMs,
		Error:          m.Error,
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
