package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/migration"
	"dbplatform/app/internal/rbac"
	"dbplatform/app/views/components"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

var fromTableRe = regexp.MustCompile(`(?i)\bFROM\s+["` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `]?`)

type Server struct {
	mux        *http.ServeMux
	handler    http.Handler
	logger     *log.Logger
	sessions   *authn.SessionStore
	groups     *groups.Repository
	access     *rbac.Authorizer
	migrations *migration.Service
	dbRegistry *dbmanager.ConnectionRegistry
}

// Deps are the services the HTTP layer needs. All are backed by the platform PostgreSQL in production.
type Deps struct {
	Sessions *authn.SessionStore // identity and session lifecycle
	Groups   *groups.Repository  // groups, membership, invite codes
	Access   *rbac.Authorizer    // every database authorization decision
	// Registry holds live pools to target databases; shared with the migration engine. Nil creates an empty one.
	Registry   *dbmanager.ConnectionRegistry
	Migrations *migration.Service // drafts, dry runs, execution and history of target migrations
}

// NewServer menyusun router beserta rantai middleware (Logger -> CSRF -> Mux)
func NewServer(logOutput io.Writer, deps Deps) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	logger := log.New(logOutput, "", log.LstdFlags)
	mux := http.NewServeMux()

	s := &Server{
		mux:        mux,
		logger:     logger,
		sessions:   deps.Sessions,
		groups:     deps.Groups,
		access:     deps.Access,
		migrations: deps.Migrations,
		dbRegistry: deps.Registry,
	}
	if s.dbRegistry == nil {
		s.dbRegistry = dbmanager.NewConnectionRegistry()
	}
	s.registerRoutes()

	var h http.Handler = s.mux
	// Request -> SanitizedLogger -> CSRFGuard -> Mux Handlers
	h = CSRFGuardMiddleWare(h)
	h = SanitizedLoggerMiddleware(s.logger)(h)

	s.handler = h
	return s
}

// Sessions mengembalikan instance SessionStore milik server
func (s *Server) Sessions() *authn.SessionStore {
	return s.sessions
}

// RegisterDatabasePool mendaftarkan koneksi *sql.DB untuk database target tertentu
func (s *Server) RegisterDatabasePool(dbID string, db *sql.DB) {
	if s.dbRegistry != nil {
		s.dbRegistry.RegisterPool(dbID, db)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Static assets (Tailwind CSS, HTMX, Alpine.js)
	fs := http.FileServer(http.Dir("./static"))
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", fs))

	// GOTTH UI & HTMX Routes
	s.mux.HandleFunc("GET /", s.handleRoot)
	s.mux.HandleFunc("GET /login", s.handleLoginPage)
	s.mux.HandleFunc("POST /login", s.handleLoginHTMX)
	s.mux.HandleFunc("GET /auth/login", s.handleLoginPage)
	s.mux.HandleFunc("POST /auth/login", s.handleLoginHTMX)
	s.mux.HandleFunc("GET /signup", s.handleSignupPage)
	s.mux.HandleFunc("POST /signup", s.handleSignupHTMX)
	s.mux.HandleFunc("GET /auth/signup", s.handleSignupPage)
	s.mux.HandleFunc("POST /auth/signup", s.handleSignupHTMX)
	s.mux.HandleFunc("GET /dashboard", s.handleDashboardPage)
	s.mux.HandleFunc("GET /dashboard/select-db", s.handleDashboardPage)
	s.mux.HandleFunc("GET /dashboard/table", s.handleDashboardTable)
	s.mux.HandleFunc("POST /dashboard/query", s.handleDashboardQuery)
	s.registerMigrationRoutes()
	s.mux.HandleFunc("GET /admin", s.handleAdminPage)
	s.mux.HandleFunc("POST /admin/users/role", s.handleAdminUpdateUserRole)
	s.mux.HandleFunc("POST /logout", s.handleLogoutHTMX)

	// JSON API Routes
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/csrf-token", s.handleGetCSRFToken)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/me", s.handleGetMe)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
}

// validateRequestSession memeriksa idle timeout, absolute expiration, dan rotasi sesi otomatis pada setiap request.
// Hanya mengidentifikasi principal; hak akses diperiksa terpisah lewat rbac (lihat authz.go).
func (s *Server) validateRequestSession(w http.ResponseWriter, r *http.Request) (*authn.User, bool) {
	sidCookie, err := r.Cookie(authn.SessionCookieName)
	if err != nil || sidCookie.Value == "" {
		return nil, false
	}

	res, err := s.sessions.ValidateAndRefresh(r.Context(), sidCookie.Value)
	if err != nil || res == nil || res.User == nil {
		clearSessionCookie(w)
		return nil, false
	}

	// Jika Session Lifecycle Engine merotasi session identifier, perbarui cookie di sisi klien
	if res.Rotated && res.NewOpaqueID != "" {
		setSessionCookie(w, res.NewOpaqueID)
	}

	return res.User, true
}

func setSessionCookie(w http.ResponseWriter, opaqueSessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    opaqueSessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authn.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- GOTTH UI & HTMX Handlers ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) ensureCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     CSRFCookieName,
			Value:    c.Value,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
		return c.Value, nil
	}

	token, err := GenerateCSRFToken()
	if err != nil {
		return "", err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	token, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(pages.LoginForm(pages.AuthFormState{})).ServeHTTP(w, r)
		return
	}

	templ.Handler(pages.LoginPage(token, pages.AuthFormState{})).ServeHTTP(w, r)
}

func (s *Server) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	token, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(pages.SignupForm(pages.AuthFormState{})).ServeHTTP(w, r)
		return
	}

	templ.Handler(pages.SignupPage(token, pages.AuthFormState{})).ServeHTTP(w, r)
}

func (s *Server) handleLoginHTMX(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("password") != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(pages.AuthFormState{
			GeneralError: "Password dilarang dikirim melalui URL",
		}).Render(r.Context(), w)
		return
	}

	// Field "identifier" berisi username atau email; "email"/"username" diterima untuk kompatibilitas.
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("username"))
	}
	if identifier == "" {
		identifier = strings.TrimSpace(r.FormValue("email"))
	}
	password := r.FormValue("password")

	state := pages.AuthFormState{Identifier: identifier}
	if identifier == "" {
		state.IdentifierError = "Username atau email wajib diisi"
	}
	if password == "" {
		state.PasswordError = "Password wajib diisi"
	}
	if state.IdentifierError != "" || state.PasswordError != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(state).Render(r.Context(), w)
		return
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, _, err := s.sessions.Login(r.Context(), identifier, password, prevToken)
	if err != nil {
		state.GeneralError = "Username/email atau password tidak valid!"
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(state).Render(r.Context(), w)
		return
	}

	setSessionCookie(w, opaqueSessionID)

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSignupHTMX(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("password") != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(pages.AuthFormState{
			GeneralError: "Password dilarang dikirim melalui URL",
		}).Render(r.Context(), w)
		return
	}

	// Sign Up publik selalu membuat akun USER. Field account_type (jika dikirim) sengaja diabaikan.
	username := authn.NormalizeUsername(r.FormValue("username"))
	email := authn.NormalizeEmail(r.FormValue("email"))
	password := r.FormValue("password")
	inviteCode := strings.TrimSpace(r.FormValue("invite_code"))

	state := pages.AuthFormState{
		Username:   username,
		Email:      email,
		InviteCode: inviteCode,
	}
	if username == "" {
		state.UsernameError = "Username wajib diisi"
	} else if err := authn.ValidateUsername(username); err != nil {
		state.UsernameError = "3-32 karakter: a-z, 0-9, titik, _ atau -"
	}
	if err := authn.ValidateEmail(email); err != nil {
		state.EmailError = "Format email tidak valid"
	}
	if password == "" {
		state.PasswordError = "Password wajib diisi"
	} else if err := authn.ValidatePassword(password); err != nil {
		state.PasswordError = "8-128 karakter"
	}
	// Kode undangan opsional: kosong berarti hanya menjadi anggota group Public (otomatis untuk semua user)
	var inviteGroup *groups.Group
	if inviteCode != "" {
		g, err := s.groups.ResolveInviteCode(r.Context(), inviteCode)
		if err != nil {
			state.CodeError = "Kode tim tidak valid"
		} else {
			inviteGroup = g
		}
	}

	if state.UsernameError != "" || state.EmailError != "" || state.PasswordError != "" || state.CodeError != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(state).Render(r.Context(), w)
		return
	}

	newUser, err := s.sessions.RegisterUser(r.Context(), authn.RegisterInput{
		Username: username,
		Email:    email,
		Password: password,
		Role:     authn.RoleUser,
	})
	switch {
	case errors.Is(err, authn.ErrUsernameTaken):
		state.UsernameError = "Username sudah terdaftar"
	case errors.Is(err, authn.ErrEmailTaken):
		state.EmailError = "Email sudah terdaftar"
	case err != nil:
		state.GeneralError = "Gagal mendaftarkan akun, silakan coba lagi."
	}
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(state).Render(r.Context(), w)
		return
	}

	if inviteGroup != nil {
		if err := s.groups.AddMember(r.Context(), inviteGroup.ID, newUser.ID); err != nil {
			// Akun tetap dibuat (anggota Public); admin dapat menambahkan group secara manual
			s.logger.Printf("[SIGNUP] gagal menambahkan user ke group %s: %v", inviteGroup.ID, err)
		}
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, _, err := s.sessions.Login(r.Context(), username, password, prevToken)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = pages.LoginForm(pages.AuthFormState{
			Identifier:   username,
			GeneralError: "Akun berhasil dibuat, silakan login.",
		}).Render(r.Context(), w)
		return
	}

	setSessionCookie(w, opaqueSessionID)

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

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

	activeDBID := resolveDBID(r, layout)
	if activeDBID == "" {
		// Tidak ada database yang dapat diakses: tampilkan empty state
		templ.Handler(pages.Dashboard(layout, "", nil, domain.TableDataResult{}, csrfToken)).ServeHTTP(w, r)
		return
	}
	if _, err := s.authorizeDB(r.Context(), user, activeDBID, rbac.ActionViewSchema); err != nil {
		s.renderForbiddenPage(w, r, layout, csrfToken, "viewer", err)
		return
	}

	var (
		tables       []domain.TableSummary
		selectedData domain.TableDataResult
	)
	if liveTables, err := s.dbRegistry.ListTables(r.Context(), activeDBID); err == nil && len(liveTables) > 0 {
		tables = liveTables
		if liveData, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), activeDBID, tables[0].Name, 1, 25); err == nil && liveData != nil {
			selectedData = *liveData
		} else {
			selectedData = buildTableDataAndSchemaForDB(activeDBID, tables[0].Name)
		}
	} else {
		tables = buildTablesForDB(activeDBID)
		firstTbl := "users"
		if len(tables) > 0 {
			firstTbl = tables[0].Name
		}
		selectedData = buildTableDataAndSchemaForDB(activeDBID, firstTbl)
	}

	templ.Handler(pages.Dashboard(layout, activeDBID, tables, selectedData, csrfToken)).ServeHTTP(w, r)
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

	tableName := r.URL.Query().Get("name")
	if tableName == "" {
		tbls := buildTablesForDB(dbID)
		if len(tbls) > 0 {
			tableName = tbls[0].Name
		} else {
			tableName = "users"
		}
	}

	page := 1
	if pStr := r.URL.Query().Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	if liveData, err := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, tableName, page, 25); err == nil && liveData != nil {
		_ = components.DataGrid(*liveData).Render(r.Context(), w)
		return
	}

	dummyData := buildTableDataAndSchemaForDB(dbID, tableName)
	_ = components.DataGrid(dummyData).Render(r.Context(), w)
}

func buildTablesForDB(dbID string) []domain.TableSummary {
	if dbID == "db-2" {
		return []domain.TableSummary{
			{Name: "orders", Schema: "analytics_db", RowCount: 320, IsSelected: true},
			{Name: "customers", Schema: "analytics_db", RowCount: 85, IsSelected: false},
			{Name: "daily_metrics", Schema: "analytics_db", RowCount: 30, IsSelected: false},
		}
	}
	return []domain.TableSummary{
		{Name: "users", Schema: "public", RowCount: 150, IsSelected: true},
		{Name: "sessions", Schema: "public", RowCount: 24, IsSelected: false},
		{Name: "groups", Schema: "public", RowCount: 12, IsSelected: false},
		{Name: "migration_histories", Schema: "public", RowCount: 45, IsSelected: false},
	}
}

func buildTableDataAndSchemaForDB(dbID, tableName string) domain.TableDataResult {
	if dbID == "db-2" {
		switch tableName {
		case "customers":
			return domain.TableDataResult{
				SchemaName: "analytics_db",
				TableName:  "customers",
				Columns:    []string{"customer_id", "full_name", "segment", "registered_at"},
				Rows: [][]string{
					{"cust-901", "PT Nusantara Digital", "ENTERPRISE", "2026-07-10 08:00"},
					{"cust-902", "CV Sinar Abadi", "SMB", "2026-07-14 13:20"},
				},
				TotalRows:   85,
				CurrentPage: 1,
				TotalPages:  4,
				ColumnsMeta: []domain.ColumnInfo{
					{Name: "customer_id", DataType: "VARCHAR(36)", IsNullable: false, IsPK: true, DefaultValue: ""},
					{Name: "full_name", DataType: "VARCHAR(120)", IsNullable: false, IsPK: false, DefaultValue: ""},
					{Name: "segment", DataType: "VARCHAR(32)", IsNullable: false, IsPK: false, DefaultValue: "'SMB'"},
					{Name: "registered_at", DataType: "DATETIME", IsNullable: false, IsPK: false, DefaultValue: "CURRENT_TIMESTAMP"},
				},
				Constraints: []domain.ConstraintInfo{
					{Name: "PRIMARY", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (customer_id)"},
				},
				Indexes: []domain.IndexInfo{
					{Name: "PRIMARY", Columns: "customer_id", IsUnique: true, IndexType: "BTREE"},
					{Name: "idx_customers_segment", Columns: "segment", IsUnique: false, IndexType: "BTREE"},
				},
			}
		case "daily_metrics":
			return domain.TableDataResult{
				SchemaName: "analytics_db",
				TableName:  "daily_metrics",
				Columns:    []string{"metric_date", "active_users", "total_orders", "revenue_idr"},
				Rows: [][]string{
					{"2026-10-24", "1,420", "310", "48500000"},
					{"2026-10-25", "1,580", "342", "53200000"},
				},
				TotalRows:   30,
				CurrentPage: 1,
				TotalPages:  2,
				ColumnsMeta: []domain.ColumnInfo{
					{Name: "metric_date", DataType: "DATE", IsNullable: false, IsPK: true, DefaultValue: ""},
					{Name: "active_users", DataType: "INT", IsNullable: false, IsPK: false, DefaultValue: "0"},
					{Name: "total_orders", DataType: "INT", IsNullable: false, IsPK: false, DefaultValue: "0"},
					{Name: "revenue_idr", DataType: "BIGINT", IsNullable: false, IsPK: false, DefaultValue: "0"},
				},
				Constraints: []domain.ConstraintInfo{
					{Name: "PRIMARY", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (metric_date)"},
				},
				Indexes: []domain.IndexInfo{
					{Name: "PRIMARY", Columns: "metric_date", IsUnique: true, IndexType: "BTREE"},
				},
			}
		default:
			return domain.TableDataResult{
				SchemaName: "analytics_db",
				TableName:  "orders",
				Columns:    []string{"order_id", "customer_id", "status", "total_amount", "created_at"},
				Rows: [][]string{
					{"ord-5001", "cust-901", "PAID", "15000000", "2026-10-24 10:15"},
					{"ord-5002", "cust-902", "PROCESSING", "4250000", "2026-10-25 09:40"},
				},
				TotalRows:   320,
				CurrentPage: 1,
				TotalPages:  13,
				ColumnsMeta: []domain.ColumnInfo{
					{Name: "order_id", DataType: "VARCHAR(36)", IsNullable: false, IsPK: true, DefaultValue: ""},
					{Name: "customer_id", DataType: "VARCHAR(36)", IsNullable: false, IsPK: false, DefaultValue: ""},
					{Name: "status", DataType: "VARCHAR(24)", IsNullable: false, IsPK: false, DefaultValue: "'PENDING'"},
					{Name: "total_amount", DataType: "DECIMAL(14,2)", IsNullable: false, IsPK: false, DefaultValue: "0.00"},
					{Name: "created_at", DataType: "DATETIME", IsNullable: false, IsPK: false, DefaultValue: "CURRENT_TIMESTAMP"},
				},
				Constraints: []domain.ConstraintInfo{
					{Name: "PRIMARY", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (order_id)"},
					{Name: "fk_orders_customer", Type: "FOREIGN KEY", Definition: "FOREIGN KEY (customer_id) REFERENCES customers(customer_id)"},
				},
				Indexes: []domain.IndexInfo{
					{Name: "PRIMARY", Columns: "order_id", IsUnique: true, IndexType: "BTREE"},
					{Name: "idx_orders_status", Columns: "status", IsUnique: false, IndexType: "BTREE"},
				},
			}
		}
	}
	return buildTableDataAndSchema(tableName)
}

func buildTableDataAndSchema(tableName string) domain.TableDataResult {
	switch tableName {
	case "groups":
		return domain.TableDataResult{
			SchemaName: "public",
			TableName:  "groups",
			Columns:    []string{"id", "name", "parent_group_id", "created_at"},
			Rows: [][]string{
				{"grp-1", "Backend-Engineers", "NULL", "2026-08-01 09:00"},
				{"grp-2", "Data-Analysts", "NULL", "2026-08-01 09:15"},
			},
			TotalRows:   12,
			CurrentPage: 1,
			TotalPages:  1,
			ColumnsMeta: []domain.ColumnInfo{
				{Name: "id", DataType: "VARCHAR(64)", IsNullable: false, IsPK: true, DefaultValue: ""},
				{Name: "name", DataType: "VARCHAR(100)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "parent_group_id", DataType: "VARCHAR(64)", IsNullable: true, IsPK: false, DefaultValue: "NULL"},
				{Name: "created_at", DataType: "TIMESTAMPTZ", IsNullable: false, IsPK: false, DefaultValue: "NOW()"},
			},
			Constraints: []domain.ConstraintInfo{
				{Name: "groups_pkey", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (id)"},
				{Name: "groups_name_key", Type: "UNIQUE", Definition: "UNIQUE (name)"},
				{Name: "groups_parent_fk", Type: "FOREIGN KEY", Definition: "FOREIGN KEY (parent_group_id) REFERENCES groups(id) ON DELETE SET NULL"},
			},
			Indexes: []domain.IndexInfo{
				{Name: "groups_pkey", Columns: "id", IsUnique: true, IndexType: "BTREE"},
				{Name: "groups_name_key", Columns: "name", IsUnique: true, IndexType: "BTREE"},
			},
		}
	case "migration_histories":
		return domain.TableDataResult{
			SchemaName: "public",
			TableName:  "migration_histories",
			Columns:    []string{"id", "version", "title", "status", "executed_by", "executed_at"},
			Rows: [][]string{
				{"mig-001", "1", "001_authn_schema", "SUCCESS", "usr-101", "2026-08-01 10:05"},
				{"mig-002", "2", "002_add_users_email", "SUCCESS", "usr-101", "2026-08-02 14:20"},
			},
			TotalRows:   45,
			CurrentPage: 1,
			TotalPages:  5,
			ColumnsMeta: []domain.ColumnInfo{
				{Name: "id", DataType: "VARCHAR(64)", IsNullable: false, IsPK: true, DefaultValue: ""},
				{Name: "version", DataType: "INT", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "title", DataType: "VARCHAR(255)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "sql_query", DataType: "TEXT", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "status", DataType: "VARCHAR(32)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "executed_by", DataType: "VARCHAR(64)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "executed_at", DataType: "TIMESTAMPTZ", IsNullable: false, IsPK: false, DefaultValue: "NOW()"},
			},
			Constraints: []domain.ConstraintInfo{
				{Name: "migration_histories_pkey", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (id)"},
				{Name: "migration_histories_exec_fk", Type: "FOREIGN KEY", Definition: "FOREIGN KEY (executed_by) REFERENCES users(id)"},
			},
			Indexes: []domain.IndexInfo{
				{Name: "migration_histories_pkey", Columns: "id", IsUnique: true, IndexType: "BTREE"},
				{Name: "idx_migration_version", Columns: "version", IsUnique: true, IndexType: "BTREE"},
			},
		}
	default:
		return domain.TableDataResult{
			SchemaName: "public",
			TableName:  "users",
			Columns:    []string{"id", "username", "email", "role", "created_at"},
			Rows: [][]string{
				{"usr-101", "moreno", "moreno@email.com", "PLATFORM_ADMIN", "2026-08-01 10:00"},
				{"usr-102", "akmal", "akmal@email.com", "USER", "2026-08-02 11:30"},
				{"usr-103", "dzaki", "dzaki@email.com", "USER", "2026-08-03 14:15"},
			},
			TotalRows:   150,
			CurrentPage: 1,
			TotalPages:  15,
			ColumnsMeta: []domain.ColumnInfo{
				{Name: "id", DataType: "VARCHAR(64)", IsNullable: false, IsPK: true, DefaultValue: ""},
				{Name: "username", DataType: "VARCHAR(64)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "email", DataType: "VARCHAR(128)", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "password_hash", DataType: "TEXT", IsNullable: false, IsPK: false, DefaultValue: ""},
				{Name: "role", DataType: "VARCHAR(32)", IsNullable: false, IsPK: false, DefaultValue: "'USER'"},
				{Name: "is_active", DataType: "BOOLEAN", IsNullable: false, IsPK: false, DefaultValue: "TRUE"},
				{Name: "created_at", DataType: "TIMESTAMPTZ", IsNullable: false, IsPK: false, DefaultValue: "NOW()"},
			},
			Constraints: []domain.ConstraintInfo{
				{Name: "users_pkey", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (id)"},
				{Name: "users_username_key", Type: "UNIQUE", Definition: "UNIQUE (username)"},
				{Name: "idx_users_email", Type: "UNIQUE", Definition: "UNIQUE (email)"},
			},
			Indexes: []domain.IndexInfo{
				{Name: "users_pkey", Columns: "id", IsUnique: true, IndexType: "BTREE"},
				{Name: "users_username_key", Columns: "username", IsUnique: true, IndexType: "BTREE"},
				{Name: "idx_users_email", Columns: "email", IsUnique: true, IndexType: "BTREE"},
			},
		}
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

	rawSQL := r.FormValue("sql_query")
	cleanedSQL, forbiddenKW, err := dbmanager.ValidateReadOnlySQL(rawSQL)
	if err != nil {
		errMsg := err.Error()
		if forbiddenKW != "" {
			errMsg = "Perintah '" + forbiddenKW + "' ditolak oleh Read-Only Guard! Hanya query SELECT yang diizinkan pada halaman utama."
		}
		rejectedData := buildTableDataAndSchemaForDB(dbID, "")
		rejectedData.QueryError = errMsg
		w.WriteHeader(http.StatusOK)
		_ = components.DataGrid(rejectedData).Render(r.Context(), w)
		return
	}

	// 1. Jika pool koneksi live untuk database yang dipilih tersedia (misal PostgreSQL Demo "db-1"),
	// jalankan query sesungguhnya ke database tersebut di dalam Read-Only Transaction!
	if _, hasPool := s.dbRegistry.GetPool(dbID); hasPool {
		liveRes, execErr := s.dbRegistry.ExecuteQueryOnDB(r.Context(), dbID, cleanedSQL)
		if execErr != nil {
			errData := buildTableDataAndSchemaForDB(dbID, "")
			errData.QueryError = fmt.Sprintf("Eksekusi query pada database '%s' gagal: %v", dbID, execErr)
			w.WriteHeader(http.StatusOK)
			_ = components.DataGrid(errData).Render(r.Context(), w)
			return
		}
		// Sertakan juga metadata schema dari tabel yang di-query jika terdeteksi pada klausa FROM
		if m := fromTableRe.FindStringSubmatch(cleanedSQL); len(m) > 1 {
			if inspected, inspectErr := s.dbRegistry.InspectTableDataAndSchema(r.Context(), dbID, m[1], 1, 25); inspectErr == nil && inspected != nil {
				liveRes.SchemaName = inspected.SchemaName
				liveRes.TableName = inspected.TableName
				liveRes.ColumnsMeta = inspected.ColumnsMeta
				liveRes.Constraints = inspected.Constraints
				liveRes.Indexes = inspected.Indexes
			}
		}
		w.WriteHeader(http.StatusOK)
		_ = components.DataGrid(*liveRes).Render(r.Context(), w)
		return
	}

	// 2. Fallback untuk database simulasi / unit test tanpa koneksi live (misal db-2 MySQL Production)
	targetTable := ""
	if m := fromTableRe.FindStringSubmatch(cleanedSQL); len(m) > 1 {
		targetTable = strings.ToLower(m[1])
	}
	resultData := buildTableDataAndSchemaForDB(dbID, targetTable)
	resultData.ExecutedQuery = fmt.Sprintf("[%s] %s", dbID, cleanedSQL)
	w.WriteHeader(http.StatusOK)
	_ = components.DataGrid(resultData).Render(r.Context(), w)
}

func (s *Server) handleLogoutHTMX(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

// --- JSON API Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "db-management-platform-api",
	})
}

// Handler Penerbitan Token CSRF untuk Browser / Frontend
func (S *Server) handleGetCSRFToken(w http.ResponseWriter, r *http.Request) {
	token, err := GenerateCSRFToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal membuat token CSRF")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"csrf_token": token,
	})
}

// Handler login JSON API
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// tolak ketika pengguna mencoba mengirim password lewat URL query
	if r.URL.Query().Get("password") != "" {
		writeJSONError(w, http.StatusBadRequest, "Password dilarang dikirim melalui URL")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Payload JSON tidak valid")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "Username dan password wajib diisi")
		return
	}

	var prevToken string
	if c, err := r.Cookie(authn.SessionCookieName); err == nil {
		prevToken = c.Value
	}

	opaqueSessionID, user, err := s.sessions.Login(r.Context(), req.Username, req.Password, prevToken)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Username atau password tidak valid")
		return
	}

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil pengguna")
		return
	}

	setSessionCookie(w, opaqueSessionID)

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "login berhasil",
		"username": user.Username,
		"user":     layout,
	})
}

// Handler mengambil profil & otorisasi user berdasarkan cookie sesi (__Host-sid)
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "Sesi tidak valid atau telah berakhir")
		return
	}

	layout, err := s.layoutUser(r.Context(), user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat profil pengguna")
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

// Handler logout untuk menghapus sesi aktif di server dan membersihkan cookie __Host-sid
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logout berhasil",
	})
}

// helper menulis response JSON secara konsisten
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
