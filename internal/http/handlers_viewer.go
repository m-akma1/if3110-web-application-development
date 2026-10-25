package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"dbplatform/domain"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/rbac"
	"dbplatform/views/components"
	"dbplatform/views/pages"
)

// Database Viewer routes. Everything shown comes from the live target; when it cannot be
// read, the page says so (with the reason and a retry) instead of showing placeholder data.
func (s *Server) registerViewerRoutes() {
	s.mux.HandleFunc("GET /dashboard", s.handleDashboardPage)
	s.mux.HandleFunc("GET /dashboard/table", s.handleDashboardTable)
	s.mux.HandleFunc("POST /dashboard/query", s.handleDashboardQuery)
}

// viewerPageSize is the number of rows per data grid page.
const viewerPageSize = 25

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
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
		// Tidak ada database yang dapat diakses: tampilkan empty state
		_ = pages.Dashboard(layout, domain.ViewerPageView{}, csrfToken).Render(r.Context(), w)
		return
	}
	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewSchema); err != nil {
		s.renderForbiddenPage(w, r, layout, csrfToken, "viewer", err)
		return
	}

	view := domain.ViewerPageView{DatabaseID: dbID, DatabaseName: databaseName(layout, dbID)}
	tables, err := s.dbRegistry.ListTables(r.Context(), dbID)
	if err != nil {
		s.logViewerError(user, dbID, "list tables", err)
		view.Error = viewerErrorMessage(err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = pages.Dashboard(layout, view, csrfToken).Render(r.Context(), w)
		return
	}
	view.Tables = tables
	if len(tables) > 0 {
		view.Tables[0].IsSelected = true
		first := tables[0]
		data, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, first.Schema, first.Name, 1, viewerPageSize)
		if err != nil {
			s.logViewerError(user, dbID, "inspect "+first.Schema+"."+first.Name, err)
			view.TableError = viewerErrorMessage(err)
		} else {
			view.Selected = data
		}
	}
	_ = pages.Dashboard(layout, view, csrfToken).Render(r.Context(), w)
}

func (s *Server) handleDashboardTable(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	dbID := resolveDBID(r, layout)
	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewData); err != nil {
		writeFragmentStatus(w, err)
		_ = components.DataGridError(accessDeniedMessage(err)).Render(r.Context(), w)
		return
	}

	q := r.URL.Query()
	schema, name := q.Get("schema"), q.Get("name")
	if name == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = components.DataGridError("Pilih tabel dari daftar di sebelah kiri.").Render(r.Context(), w)
		return
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	data, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, schema, name, page, viewerPageSize)
	switch {
	case errors.Is(err, dbmanager.ErrTableNotFound):
		w.WriteHeader(http.StatusNotFound)
		_ = components.DataGridError("Tabel tidak ditemukan. Tabel mungkin sudah dihapus atau dipindahkan; muat ulang halaman untuk memperbarui daftar.").Render(r.Context(), w)
	case err != nil:
		s.logViewerError(user, dbID, "inspect "+schema+"."+name, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = components.DataGridUnavailable(viewerErrorTitle(err), viewerErrorMessage(err),
			components.TableURL(dbID, schema, name, page)).Render(r.Context(), w)
	default:
		_ = components.DataGrid(*data).Render(r.Context(), w)
	}
}

func (s *Server) handleDashboardQuery(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	// Otorisasi selalu lewat rbac: membaca data butuh minimal VIEW_ONLY melalui group
	dbID := resolveDBID(r, layout)
	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewData); err != nil {
		writeFragmentStatus(w, err)
		_ = components.DataGridError(accessDeniedMessage(err)).Render(r.Context(), w)
		return
	}

	cleanedSQL, forbidden, err := dbmanager.ValidateReadOnlySQL(r.FormValue("sql_query"))
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = components.DataGridError(guardMessage(err, forbidden)).Render(r.Context(), w)
		return
	}

	res, err := s.dbRegistry.ExecuteQueryOnDB(r.Context(), dbID, cleanedSQL)
	switch {
	case errors.Is(err, dbmanager.ErrNoPool):
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = components.DataGridUnavailable(viewerErrorTitle(err), viewerErrorMessage(err), "").Render(r.Context(), w)
		return
	case err != nil:
		// Usually a mistake in the query (syntax, unknown column, timeout): the user can fix it.
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = components.DataGridError("Eksekusi query gagal: "+err.Error()).Render(r.Context(), w)
		return
	}

	// The schema panel only makes sense when the result comes from exactly one table.
	s.attachQuerySchema(r.Context(), res, cleanedSQL)
	_ = components.DataGrid(*res).Render(r.Context(), w)
}

// attachQuerySchema adds the columns, constraints and indexes of the queried table when the
// query is a plain single-table SELECT. Failing to describe it only hides the panel.
func (s *Server) attachQuerySchema(ctx context.Context, res *domain.TableDataResult, query string) {
	schema, table, ok := dbmanager.SingleTableSource(query)
	if !ok {
		return
	}
	meta, err := s.dbRegistry.DescribeTable(ctx, res.DatabaseID, schema, table)
	if err != nil {
		return
	}
	res.SchemaName = meta.SchemaName
	res.TableName = meta.TableName
	res.ColumnsMeta = meta.ColumnsMeta
	res.Constraints = meta.Constraints
	res.Indexes = meta.Indexes
	res.HasSchema = true
}

func guardMessage(err error, forbidden string) string {
	switch {
	case errors.Is(err, dbmanager.ErrBlockedFunction):
		return "Fungsi '" + forbidden + "()' ditolak oleh Read-Only Guard: fungsi server ini tidak diizinkan pada Data Viewer."
	case forbidden != "":
		return "Perintah '" + forbidden + "' ditolak oleh Read-Only Guard! Hanya query SELECT yang diizinkan pada halaman utama."
	default:
		return err.Error()
	}
}

func viewerErrorTitle(err error) string {
	if errors.Is(err, dbmanager.ErrNoPool) {
		return "Database tidak tersedia"
	}
	return "Gagal membaca database"
}

func viewerErrorMessage(err error) string {
	if errors.Is(err, dbmanager.ErrNoPool) {
		return "Database ini belum terhubung ke platform. Platform Admin dapat menguji ulang koneksinya di halaman Admin → Databases."
	}
	return err.Error()
}

func (s *Server) logViewerError(user *authn.User, dbID, op string, err error) {
	s.logger.Printf("[VIEWER] %s gagal user=%s db=%s: %v", op, user.ID, dbID, err)
}

func databaseName(layout *domain.AuthLayoutUser, dbID string) string {
	if layout == nil {
		return ""
	}
	for _, db := range layout.Databases {
		if db.DatabaseID == dbID {
			return db.DatabaseName
		}
	}
	return ""
}
