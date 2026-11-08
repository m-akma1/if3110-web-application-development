package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"dbplatform/domain"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/rbac"
)

const viewerPageSize = 25

func (s *Server) handleAPIViewer(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	dbID := resolveDBID(r, layout)
	if dbID == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"database_id":   "",
			"database_name": "",
			"tables":        []domain.TableSummary{},
			"selected":      nil,
		})
		return
	}

	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewSchema); err != nil {
		writeJSONError(w, http.StatusForbidden, accessDeniedMessage(err))
		return
	}

	dbName := databaseName(layout, dbID)
	tables, err := s.dbRegistry.ListTables(r.Context(), dbID)
	if err != nil {
		s.logViewerError(user, dbID, "list tables", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"database_id":   dbID,
			"database_name": dbName,
			"tables":        []domain.TableSummary{},
			"error":         viewerErrorMessage(err),
		})
		return
	}

	var selected *domain.TableDataResult
	var tableError string
	if len(tables) > 0 {
		tables[0].IsSelected = true
		first := tables[0]
		data, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, first.Schema, first.Name, 1, viewerPageSize)
		if err != nil {
			s.logViewerError(user, dbID, "inspect "+first.Schema+"."+first.Name, err)
			tableError = viewerErrorMessage(err)
		} else {
			selected = data
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"database_id":   dbID,
		"database_name": dbName,
		"tables":        tables,
		"selected":      selected,
		"table_error":   tableError,
	})
}

func (s *Server) handleAPIViewerTable(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	dbID := resolveDBID(r, layout)
	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewData); err != nil {
		writeJSONError(w, http.StatusForbidden, accessDeniedMessage(err))
		return
	}

	q := r.URL.Query()
	schema, name := q.Get("schema"), q.Get("name")
	if name == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "Pilih tabel dari daftar di sebelah kiri.")
		return
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	data, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, schema, name, page, viewerPageSize)
	switch {
	case errors.Is(err, dbmanager.ErrTableNotFound):
		writeJSONError(w, http.StatusNotFound, "Tabel tidak ditemukan. Tabel mungkin sudah dihapus atau dipindahkan.")
	case err != nil:
		s.logViewerError(user, dbID, "inspect "+schema+"."+name, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": viewerErrorMessage(err),
			"title": viewerErrorTitle(err),
		})
	default:
		writeJSON(w, http.StatusOK, data)
	}
}

type apiViewerQueryRequest struct {
	DatabaseID string `json:"db_id"`
	SQLQuery   string `json:"sql_query"`
}

func (s *Server) handleAPIViewerQuery(w http.ResponseWriter, r *http.Request) {
	user, layout, ok := s.requireSession(w, r)
	if !ok {
		return
	}

	var req apiViewerQueryRequest
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := readJSONBody(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}
	} else {
		req.DatabaseID = r.FormValue("db_id")
		req.SQLQuery = r.FormValue("sql_query")
	}

	dbID := req.DatabaseID
	if dbID == "" {
		dbID = resolveDBID(r, layout)
	}

	if _, err := s.authorizeDB(r.Context(), user, dbID, rbac.ActionViewData); err != nil {
		writeJSONError(w, http.StatusForbidden, accessDeniedMessage(err))
		return
	}

	cleanedSQL, forbidden, err := dbmanager.ValidateReadOnlySQL(req.SQLQuery)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": guardMessage(err, forbidden),
		})
		return
	}

	res, err := s.dbRegistry.ExecuteQueryOnDB(r.Context(), dbID, cleanedSQL)
	switch {
	case errors.Is(err, dbmanager.ErrNoPool):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": viewerErrorMessage(err),
			"title": viewerErrorTitle(err),
		})
		return
	case err != nil:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "Eksekusi query gagal: " + err.Error(),
		})
		return
	}

	s.attachQuerySchema(r.Context(), res, cleanedSQL)
	writeJSON(w, http.StatusOK, res)
}

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
