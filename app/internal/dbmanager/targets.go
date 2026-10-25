package dbmanager

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dbplatform/app/internal/ids"

	"github.com/jackc/pgx/v5/pgconn"
)

// Target database connection status, as last observed by the platform.
const (
	StatusUnknown   = "UNKNOWN"
	StatusConnected = "CONNECTED"
	StatusError     = "ERROR"
)

var (
	ErrTargetNotFound  = errors.New("database target tidak ditemukan")
	ErrTargetNameTaken = errors.New("nama database target sudah dipakai")
)

// ValidationError reports one invalid field of a target form.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// Target is a PostgreSQL database registered by the Platform Admin. The password is never part of it.
type Target struct {
	ID            string
	Name          string
	Host          string
	Port          int
	DBName        string
	Username      string
	SSLMode       string
	Status        string
	LastError     string
	LastCheckedAt *time.Time
	CreatedAt     time.Time
}

// TargetInput is the admin form for creating or editing a target.
type TargetInput struct {
	Name     string
	Host     string
	Port     int
	DBName   string
	Username string
	Password string // empty on edit means "keep the stored password"
	SSLMode  string
}

var sslModes = map[string]bool{"disable": true, "require": true, "verify-full": true}

// Normalize trims fields and fills defaults (port 5432, sslmode disable).
func (in TargetInput) Normalize() TargetInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.DBName = strings.TrimSpace(in.DBName)
	in.Username = strings.TrimSpace(in.Username)
	in.SSLMode = strings.TrimSpace(in.SSLMode)
	if in.Port == 0 {
		in.Port = 5432
	}
	if in.SSLMode == "" {
		in.SSLMode = "disable"
	}
	return in
}

// Validate checks a normalized input. requirePassword is true when creating.
func (in TargetInput) Validate(requirePassword bool) error {
	switch {
	case len(in.Name) < 2 || len(in.Name) > 100:
		return &ValidationError{"name", "nama tampilan harus 2-100 karakter"}
	case in.Host == "" || len(in.Host) > 255 || strings.ContainsAny(in.Host, " /?#@"):
		return &ValidationError{"host", "host tidak valid"}
	case in.Port < 1 || in.Port > 65535:
		return &ValidationError{"port", "port harus 1-65535"}
	case in.DBName == "" || len(in.DBName) > 63:
		return &ValidationError{"db_name", "nama database wajib diisi (maks. 63 karakter)"}
	case in.Username == "" || len(in.Username) > 63:
		return &ValidationError{"username", "username wajib diisi (maks. 63 karakter)"}
	case requirePassword && in.Password == "":
		return &ValidationError{"password", "password wajib diisi"}
	case !sslModes[in.SSLMode]:
		return &ValidationError{"ssl_mode", "mode SSL tidak dikenal"}
	}
	return nil
}

// DSN builds a pgx connection URL. Credentials are URL-escaped.
func DSN(t Target, password string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(t.Username, password),
		Host:   t.Host + ":" + strconv.Itoa(t.Port),
		Path:   "/" + t.DBName,
	}
	q := url.Values{}
	q.Set("sslmode", t.SSLMode)
	q.Set("connect_timeout", "5")
	q.Set("application_name", "dbplatform")
	u.RawQuery = q.Encode()
	return u.String()
}

// TargetInputFromURL turns postgres://user:pass@host:port/db?sslmode=... into a TargetInput named name.
func TargetInputFromURL(name, raw string) (TargetInput, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return TargetInput{}, errors.New("URL harus berbentuk postgres://user:password@host:port/dbname")
	}
	password, _ := u.User.Password()
	port := 5432
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return TargetInput{}, errors.New("port pada URL tidak valid")
		}
	}
	return TargetInput{
		Name:     name,
		Host:     u.Hostname(),
		Port:     port,
		DBName:   strings.TrimPrefix(u.Path, "/"),
		Username: u.User.Username(),
		Password: password,
		SSLMode:  u.Query().Get("sslmode"),
	}, nil
}

// TargetRepository stores targets on the platform DB.
type TargetRepository struct {
	db *sql.DB
}

func NewTargetRepository(db *sql.DB) *TargetRepository {
	return &TargetRepository{db: db}
}

const targetColumns = `id, name, host, port, db_name, username, ssl_mode, status, last_error, last_checked_at, created_at`

func scanTarget(row interface{ Scan(...any) error }) (*Target, error) {
	var t Target
	var checked sql.NullTime
	if err := row.Scan(&t.ID, &t.Name, &t.Host, &t.Port, &t.DBName, &t.Username, &t.SSLMode,
		&t.Status, &t.LastError, &checked, &t.CreatedAt); err != nil {
		return nil, err
	}
	if checked.Valid {
		c := checked.Time
		t.LastCheckedAt = &c
	}
	return &t, nil
}

func mapTargetErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "target_databases_name_key" {
		return ErrTargetNameTaken
	}
	return err
}

func (r *TargetRepository) Create(ctx context.Context, t *Target, sealedPassword []byte) error {
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO target_databases (id, name, host, port, db_name, username, ssl_mode, encrypted_password, status, last_error, last_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at`,
		t.ID, t.Name, t.Host, t.Port, t.DBName, t.Username, t.SSLMode, sealedPassword, t.Status, t.LastError, t.LastCheckedAt,
	).Scan(&t.CreatedAt)
	return mapTargetErr(err)
}

// Update saves connection fields and status. A nil sealedPassword keeps the stored one.
func (r *TargetRepository) Update(ctx context.Context, t *Target, sealedPassword []byte) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE target_databases
		SET name = $2, host = $3, port = $4, db_name = $5, username = $6, ssl_mode = $7,
		    encrypted_password = COALESCE($8, encrypted_password),
		    status = $9, last_error = $10, last_checked_at = $11, updated_at = NOW()
		WHERE id = $1`,
		t.ID, t.Name, t.Host, t.Port, t.DBName, t.Username, t.SSLMode, sealedPassword, t.Status, t.LastError, t.LastCheckedAt)
	if err != nil {
		return mapTargetErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTargetNotFound
	}
	return nil
}

func (r *TargetRepository) Get(ctx context.Context, id string) (*Target, error) {
	if !ids.IsValid(id) {
		return nil, ErrTargetNotFound
	}
	t, err := scanTarget(r.db.QueryRowContext(ctx, `SELECT `+targetColumns+` FROM target_databases WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTargetNotFound
	}
	return t, err
}

func (r *TargetRepository) GetByName(ctx context.Context, name string) (*Target, error) {
	t, err := scanTarget(r.db.QueryRowContext(ctx,
		`SELECT `+targetColumns+` FROM target_databases WHERE LOWER(name) = LOWER($1)`, strings.TrimSpace(name)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTargetNotFound
	}
	return t, err
}

func (r *TargetRepository) List(ctx context.Context) ([]Target, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+targetColumns+` FROM target_databases ORDER BY LOWER(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

func (r *TargetRepository) SealedPassword(ctx context.Context, id string) ([]byte, error) {
	var sealed []byte
	err := r.db.QueryRowContext(ctx, `SELECT encrypted_password FROM target_databases WHERE id = $1`, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTargetNotFound
	}
	return sealed, err
}

func (r *TargetRepository) SetStatus(ctx context.Context, id, status, lastError string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE target_databases SET status = $2, last_error = $3, last_checked_at = $4 WHERE id = $1`,
		id, status, lastError, at)
	return err
}

// Delete removes a target; its group grants go with it (ON DELETE CASCADE).
func (r *TargetRepository) Delete(ctx context.Context, id string) error {
	if !ids.IsValid(id) {
		return ErrTargetNotFound
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM target_databases WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTargetNotFound
	}
	return nil
}

// Manager is the platform admin's view of target databases: it stores them (with sealed
// passwords), checks connectivity, and keeps the ConnectionRegistry in sync.
// It knows nothing about who may use a database; that is internal/rbac.
type Manager struct {
	repo     *TargetRepository
	cipher   *CredentialCipher
	registry *ConnectionRegistry
	now      func() time.Time
}

func NewManager(repo *TargetRepository, cipher *CredentialCipher, registry *ConnectionRegistry) *Manager {
	return &Manager{repo: repo, cipher: cipher, registry: registry, now: time.Now}
}

// Registry returns the registry holding live pools (used by the viewer).
func (m *Manager) Registry() *ConnectionRegistry { return m.registry }

func (m *Manager) List(ctx context.Context) ([]Target, error) { return m.repo.List(ctx) }

func (m *Manager) Get(ctx context.Context, id string) (*Target, error) { return m.repo.Get(ctx, id) }

// Create validates, tests the connection, and stores the target. A target whose connection
// test fails is still saved, with status ERROR and the reason, so the admin can fix it.
func (m *Manager) Create(ctx context.Context, in TargetInput) (*Target, error) {
	in = in.Normalize()
	if err := in.Validate(true); err != nil {
		return nil, err
	}
	t := &Target{ID: ids.New(), Name: in.Name, Host: in.Host, Port: in.Port, DBName: in.DBName, Username: in.Username, SSLMode: in.SSLMode}
	sealed, err := m.cipher.Seal(in.Password, t.ID)
	if err != nil {
		return nil, err
	}
	pool := m.check(ctx, t, in.Password)
	if err := m.repo.Create(ctx, t, sealed); err != nil {
		if pool != nil {
			_ = pool.Close()
		}
		return nil, err
	}
	m.swapPool(t.ID, pool)
	return t, nil
}

// Update edits connection fields (empty password keeps the stored one) and re-tests the connection.
func (m *Manager) Update(ctx context.Context, id string, in TargetInput) (*Target, error) {
	current, err := m.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	in = in.Normalize()
	if err := in.Validate(false); err != nil {
		return nil, err
	}

	password := in.Password
	var sealed []byte
	if password == "" {
		if password, err = m.storedPassword(ctx, id); err != nil {
			return nil, err
		}
	} else if sealed, err = m.cipher.Seal(password, id); err != nil {
		return nil, err
	}

	t := &Target{ID: id, Name: in.Name, Host: in.Host, Port: in.Port, DBName: in.DBName, Username: in.Username, SSLMode: in.SSLMode, CreatedAt: current.CreatedAt}
	pool := m.check(ctx, t, password)
	if err := m.repo.Update(ctx, t, sealed); err != nil {
		if pool != nil {
			_ = pool.Close()
		}
		return nil, err
	}
	m.swapPool(id, pool)
	return t, nil
}

// Test reconnects one target and records the result.
func (m *Manager) Test(ctx context.Context, id string) (*Target, error) {
	t, err := m.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	password, err := m.storedPassword(ctx, id)
	if err != nil {
		m.record(ctx, t, StatusError, err.Error())
		m.swapPool(id, nil)
		return t, nil
	}
	pool := m.check(ctx, t, password)
	if err := m.repo.SetStatus(ctx, id, t.Status, t.LastError, *t.LastCheckedAt); err != nil {
		if pool != nil {
			_ = pool.Close()
		}
		return nil, err
	}
	m.swapPool(id, pool)
	return t, nil
}

// Delete closes the pool and removes the target (and, by cascade, its grants).
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.repo.Delete(ctx, id); err != nil {
		return err
	}
	m.registry.Remove(id)
	return nil
}

// LoadAll connects every stored target (at startup). Failures are recorded as ERROR, never fatal.
func (m *Manager) LoadAll(ctx context.Context) (connected, failed int, err error) {
	targets, err := m.repo.List(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, t := range targets {
		if _, err := m.Test(ctx, t.ID); err != nil {
			return connected, failed, err
		}
		if _, ok := m.registry.GetPool(t.ID); ok {
			connected++
		} else {
			failed++
		}
	}
	return connected, failed, nil
}

func (m *Manager) storedPassword(ctx context.Context, id string) (string, error) {
	sealed, err := m.repo.SealedPassword(ctx, id)
	if err != nil {
		return "", err
	}
	return m.cipher.Open(sealed, id)
}

// check opens and pings a pool, filling t.Status/LastError/LastCheckedAt. It returns the open
// pool on success, nil on failure.
func (m *Manager) check(ctx context.Context, t *Target, password string) *sql.DB {
	pool, err := openPool(ctx, DSN(*t, password))
	if err != nil {
		m.fill(t, StatusError, sanitizeConnError(err, password))
		return nil
	}
	m.fill(t, StatusConnected, "")
	return pool
}

func (m *Manager) fill(t *Target, status, lastErr string) {
	now := m.now()
	t.Status, t.LastError, t.LastCheckedAt = status, lastErr, &now
}

func (m *Manager) record(ctx context.Context, t *Target, status, lastErr string) {
	m.fill(t, status, lastErr)
	_ = m.repo.SetStatus(ctx, t.ID, status, lastErr, *t.LastCheckedAt)
}

func (m *Manager) swapPool(id string, pool *sql.DB) {
	if pool == nil {
		m.registry.Remove(id)
		return
	}
	m.registry.Replace(id, pool)
}

// openPool opens a small connection pool and verifies it with a ping.
func openPool(ctx context.Context, dsn string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(2)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	pool.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

// sanitizeConnError makes a driver error safe to store and show: never the password, bounded length.
func sanitizeConnError(err error, password string) string {
	msg := err.Error()
	if password != "" {
		msg = strings.ReplaceAll(msg, password, "[REDACTED]")
		msg = strings.ReplaceAll(msg, url.QueryEscape(password), "[REDACTED]")
	}
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}
