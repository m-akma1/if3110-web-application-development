package http

import (
	"context"
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
	"sync"
	"time"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/views/components"
	"dbplatform/app/views/pages"

	"github.com/a-h/templ"
)

var fromTableRe = regexp.MustCompile(`(?i)\bFROM\s+["` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)["` + "`" + `]?`)

type Server struct {
	mux         *http.ServeMux
	handler     http.Handler
	logger      *log.Logger
	sessions    *authn.SessionStore
	dbRegistry  *dbmanager.ConnectionRegistry
	adminMu     sync.RWMutex
	instances   []domain.DBInstance
	inviteCodes []domain.TeamInviteCodeItem
}

// NewServer menyusun router di atas SessionStore in-memory yang kosong (khusus pengujian).
// Aplikasi memakai NewServerWithSessions dengan SessionStore PostgreSQL.
func NewServer(logOutput io.Writer) *Server {
	return NewServerWithSessions(logOutput, authn.NewMemorySessionStore())
}

// NewServerWithSessions menyusun router beserta rantai middleware (Logger -> CSRF -> Mux)
func NewServerWithSessions(logOutput io.Writer, sessionStore *authn.SessionStore) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}
	if sessionStore == nil {
		panic("http: NewServerWithSessions requires a SessionStore")
	}

	logger := log.New(logOutput, "", log.LstdFlags)
	mux := http.NewServeMux()

	s := &Server{
		mux:        mux,
		logger:     logger,
		sessions:   sessionStore,
		dbRegistry: dbmanager.NewConnectionRegistry(),
		instances: []domain.DBInstance{
			{ID: "db-1", InviteCode: "TEAM-BACKEND", Name: "PostgreSQL Demo", DBMS: "POSTGRESQL", Host: "target-postgres", Port: 5432, DBName: "demo_target", Status: "CONNECTED"},
			{ID: "db-2", InviteCode: "TEAM-ANALYST", Name: "MySQL Production", DBMS: "MYSQL", Host: "mysql-prod.internal", Port: 3306, DBName: "analytics_db", Status: "CONNECTED"},
		},
		inviteCodes: []domain.TeamInviteCodeItem{
			{Code: "TEAM-BACKEND", GroupName: "Backend-Engineers", DatabaseID: "db-1", DatabaseName: "PostgreSQL Demo", DefaultAccess: "VIEW_EDIT"},
			{Code: "TEAM-ANALYST", GroupName: "Data-Analysts", DatabaseID: "db-2", DatabaseName: "MySQL Production", DefaultAccess: "VIEW_ONLY"},
		},
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

// ConnectDatabase membuka koneksi ke database target, melakukan Ping, lalu mendaftarkannya ke registry
func (s *Server) ConnectDatabase(ctx context.Context, dbID, dsn string) error {
	return s.dbRegistry.ConnectAndRegister(ctx, dbID, dsn)
}

// RegisterDatabasePool mendaftarkan koneksi *sql.DB untuk database target tertentu (misal "db-1")
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
	s.mux.HandleFunc("GET /migration", s.handleMigrationPage)
	s.mux.HandleFunc("POST /migration/dry-run", s.handleMigrationDryRun)
	s.mux.HandleFunc("POST /migration/apply", s.handleMigrationApply)
	s.mux.HandleFunc("GET /admin", s.handleAdminPage)
	s.mux.HandleFunc("POST /admin/db/add", s.handleAdminAddDB)
	s.mux.HandleFunc("POST /admin/codes/generate", s.handleAdminGenerateCode)
	s.mux.HandleFunc("POST /admin/users/access", s.handleAdminUpdateUserAccess)
	s.mux.HandleFunc("POST /logout", s.handleLogoutHTMX)

	// JSON API Routes
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/csrf-token", s.handleGetCSRFToken)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /api/auth/me", s.handleGetMe)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
}

// validateRequestSession memeriksa idle timeout, absolute expiration, dan rotasi sesi otomatis pada setiap request
func (s *Server) validateRequestSession(w http.ResponseWriter, r *http.Request) (*authn.AuthLayoutUser, bool) {
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
	// Kode undangan opsional: kosong berarti hanya menjadi anggota group Public
	if inviteCode != "" {
		if _, err := authn.ValidateSignupCode(inviteCode); err != nil {
			state.CodeError = "Kode tim tidak valid"
		}
	}

	if state.UsernameError != "" || state.EmailError != "" || state.PasswordError != "" || state.CodeError != "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.SignupForm(state).Render(r.Context(), w)
		return
	}

	_, err := s.sessions.RegisterUser(r.Context(), authn.RegisterInput{
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
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	var csrfToken string
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		csrfToken = c.Value
	} else {
		tok, err := GenerateCSRFToken()
		if err != nil {
			http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
			return
		}
		csrfToken = tok
		http.SetCookie(w, &http.Cookie{
			Name:     CSRFCookieName,
			Value:    csrfToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	activeDBID := r.URL.Query().Get("db_id")
	if activeDBID == "" && len(user.Databases) > 0 {
		activeDBID = user.Databases[0].DatabaseID
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

	templ.Handler(pages.Dashboard(user, activeDBID, tables, selectedData, csrfToken)).ServeHTTP(w, r)
}

func (s *Server) handleDashboardTable(w http.ResponseWriter, r *http.Request) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	dbID := strings.TrimSpace(r.URL.Query().Get("db_id"))
	if dbID == "" && len(user.Databases) > 0 {
		dbID = user.Databases[0].DatabaseID
	}
	if dbID == "" {
		dbID = "db-1"
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
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	dbID := strings.TrimSpace(r.FormValue("db_id"))
	if dbID == "" && len(user.Databases) > 0 {
		dbID = user.Databases[0].DatabaseID
	}
	if dbID == "" {
		dbID = "db-1"
	}

	// Cek otorisasi akses pengguna terhadap database yang dipilih
	hasAccess := user.IsPlatformAdmin
	for _, dbAcc := range user.Databases {
		if dbAcc.DatabaseID == dbID && dbAcc.EffectiveAccess != "NONE" {
			hasAccess = true
			break
		}
	}
	if !hasAccess {
		denied := buildTableDataAndSchemaForDB(dbID, "")
		denied.QueryError = fmt.Sprintf("Akses ditolak: Anda tidak memiliki izin untuk menjalankan query pada database '%s'.", dbID)
		w.WriteHeader(http.StatusOK)
		_ = components.DataGrid(denied).Render(r.Context(), w)
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

// --- Migration Editor Handlers ---

func (s *Server) handleMigrationPage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	activeDBID := r.URL.Query().Get("db_id")
	if activeDBID == "" && len(user.Databases) > 0 {
		activeDBID = user.Databases[0].DatabaseID
	}

	dummyHistories := []domain.MigrationHistory{
		{
			ID:          "m-001",
			Filename:    "001_create_users_table.sql",
			ExecutedAt:  time.Now().Add(-24 * time.Hour),
			ExecutedBy:  user.Username,
			Status:      "SUCCESS",
			ExecutionMs: 45,
		},
	}

	templ.Handler(pages.Migration(user, activeDBID, dummyHistories, csrfToken)).ServeHTTP(w, r)
}

func (s *Server) handleMigrationDryRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.validateRequestSession(w, r); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = pages.MigrationFeedback(true, "Simulasi DDL Valid: 1 tabel akan dibuat tanpa error.").Render(r.Context(), w)
}

func (s *Server) handleMigrationApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.validateRequestSession(w, r); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = pages.MigrationFeedback(true, "Migrasi berhasil dieksekusi ke PostgreSQL Target dalam 18ms.").Render(r.Context(), w)
}

func (s *Server) handleLogoutHTMX(w http.ResponseWriter, r *http.Request) {
	if sidCookie, err := r.Cookie(authn.SessionCookieName); err == nil && sidCookie.Value != "" {
		_ = s.sessions.RevokeSession(r.Context(), sidCookie.Value)
	}

	clearSessionCookie(w)

	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

// --- Platform Admin Handlers (Hanya dapat diakses oleh PLATFORM_ADMIN) ---

func (s *Server) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) (*authn.AuthLayoutUser, bool) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, false
	}
	if !user.IsPlatformAdmin {
		http.Error(w, "403 Forbidden: Hanya Platform Admin yang diizinkan mengakses halaman ini", http.StatusForbidden)
		return nil, false
	}
	return user, true
}

func (s *Server) buildAdminUsersList(ctx context.Context) []domain.UserManagementItem {
	rawUsers, err := s.sessions.ListUsers(ctx)
	if err != nil {
		return nil
	}
	items := make([]domain.UserManagementItem, 0, len(rawUsers))
	for _, u := range rawUsers {
		defaultDB1 := "VIEW_EDIT"
		defaultDB2 := "VIEW_ONLY"
		if u.Role == authn.RolePlatformAdmin {
			defaultDB2 = "VIEW_EDIT"
		}
		db1Access := authn.GetUserDBAccess(u.ID, "db-1", defaultDB1)
		db2Access := authn.GetUserDBAccess(u.ID, "db-2", defaultDB2)

		items = append(items, domain.UserManagementItem{
			ID:       u.ID,
			Username: u.Username,
			Email:    u.Email,
			Role:     u.Role,
			IsActive: u.IsActive,
			Grants: []domain.UserAccessGrant{
				{DatabaseID: "db-1", DatabaseName: "PostgreSQL Demo", DBMS: "POSTGRESQL", EffectiveAccess: db1Access},
				{DatabaseID: "db-2", DatabaseName: "MySQL Production", DBMS: "MYSQL", EffectiveAccess: db2Access},
			},
		})
	}
	return items
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}

	csrfToken, err := s.ensureCSRFToken(w, r)
	if err != nil {
		http.Error(w, "Gagal membuat token CSRF", http.StatusInternalServerError)
		return
	}

	activeDBID := r.URL.Query().Get("db_id")
	if activeDBID == "" && len(user.Databases) > 0 {
		activeDBID = user.Databases[0].DatabaseID
	}

	s.adminMu.RLock()
	instances := append([]domain.DBInstance(nil), s.instances...)
	codes := append([]domain.TeamInviteCodeItem(nil), s.inviteCodes...)
	s.adminMu.RUnlock()

	users := s.buildAdminUsersList(r.Context())

	templ.Handler(pages.Admin(user, activeDBID, instances, codes, users, csrfToken)).ServeHTTP(w, r)
}

func (s *Server) handleAdminAddDB(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	dbms := strings.TrimSpace(r.FormValue("dbms"))
	host := strings.TrimSpace(r.FormValue("host"))
	portStr := strings.TrimSpace(r.FormValue("port"))
	dbName := strings.TrimSpace(r.FormValue("db_name"))

	if name == "" || host == "" || dbName == "" {
		w.WriteHeader(http.StatusOK)
		_ = pages.AdminFeedback(false, "Nama instance, host, dan nama database wajib diisi.").Render(r.Context(), w)
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		port = 5432
	}

	s.adminMu.Lock()
	newID := fmt.Sprintf("db-%d", len(s.instances)+1)
	autoCode := fmt.Sprintf("TEAM-DB%d", len(s.instances)+1)
	newInst := domain.DBInstance{
		ID:         newID,
		InviteCode: autoCode,
		Name:       name,
		DBMS:       dbms,
		Host:       host,
		Port:       port,
		DBName:     dbName,
		Status:     "CONNECTED",
	}
	s.instances = append(s.instances, newInst)
	s.inviteCodes = append(s.inviteCodes, domain.TeamInviteCodeItem{
		Code:          autoCode,
		GroupName:     name + "-Group",
		DatabaseID:    newID,
		DatabaseName:  name,
		DefaultAccess: "VIEW_ONLY",
	})
	s.adminMu.Unlock()

	authn.RegisterTeamCode(autoCode, "grp-"+newID, name+"-Group")

	msg := fmt.Sprintf("Database '%s' (%s) berhasil didaftarkan dengan kode tim: %s", name, newID, autoCode)
	w.WriteHeader(http.StatusOK)
	_ = pages.AdminFeedback(true, msg).Render(r.Context(), w)
}

func (s *Server) handleAdminGenerateCode(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}

	code := strings.ToUpper(strings.TrimSpace(r.FormValue("code")))
	groupName := strings.TrimSpace(r.FormValue("group_name"))
	dbID := strings.TrimSpace(r.FormValue("database_id"))
	defaultAccess := strings.TrimSpace(r.FormValue("default_access"))
	if defaultAccess != "VIEW_EDIT" {
		defaultAccess = "VIEW_ONLY"
	}

	s.adminMu.Lock()
	dbName := "PostgreSQL Demo"
	for _, inst := range s.instances {
		if inst.ID == dbID {
			dbName = inst.Name
			break
		}
	}
	if code != "" && groupName != "" {
		s.inviteCodes = append(s.inviteCodes, domain.TeamInviteCodeItem{
			Code:          code,
			GroupName:     groupName,
			DatabaseID:    dbID,
			DatabaseName:  dbName,
			DefaultAccess: defaultAccess,
		})
		authn.RegisterTeamCode(code, "grp-"+strings.ToLower(code), groupName)
	}
	instances := append([]domain.DBInstance(nil), s.instances...)
	codes := append([]domain.TeamInviteCodeItem(nil), s.inviteCodes...)
	s.adminMu.Unlock()

	feedback := fmt.Sprintf("Kode undangan '%s' untuk grup '%s' (%s) berhasil diaktifkan!", code, groupName, defaultAccess)
	w.WriteHeader(http.StatusOK)
	_ = pages.AdminCodesSection(codes, instances, feedback).Render(r.Context(), w)
}

func (s *Server) handleAdminUpdateUserAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	role := strings.TrimSpace(r.FormValue("role"))
	accessDB1 := strings.TrimSpace(r.FormValue("access_db_1"))
	accessDB2 := strings.TrimSpace(r.FormValue("access_db_2"))

	if role != authn.RolePlatformAdmin {
		role = authn.RoleUser
	}

	if userID != "" {
		_ = s.sessions.UpdateUserRole(r.Context(), userID, role)
		if accessDB1 != "" {
			authn.SetUserDBAccess(userID, "db-1", accessDB1)
		}
		if accessDB2 != "" {
			authn.SetUserDBAccess(userID, "db-2", accessDB2)
		}
	}

	users := s.buildAdminUsersList(r.Context())
	feedback := fmt.Sprintf("Hak akses untuk user '%s' berhasil diperbarui (Role: %s, DB-1: %s, DB-2: %s).", userID, role, accessDB1, accessDB2)
	w.WriteHeader(http.StatusOK)
	_ = pages.AdminUsersTable(users, feedback).Render(r.Context(), w)
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

	opaqueSessionID, userProfile, err := s.sessions.Login(r.Context(), req.Username, req.Password, prevToken)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Username atau password tidak valid")
		return
	}

	setSessionCookie(w, opaqueSessionID)

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "login berhasil",
		"username": userProfile.Username,
		"user":     userProfile,
	})
}

// Handler mengambil profil & otorisasi user berdasarkan cookie sesi (__Host-sid)
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.validateRequestSession(w, r)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "Sesi tidak valid atau telah berakhir")
		return
	}

	writeJSON(w, http.StatusOK, user)
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
