package authn

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// Repository mengabstraksi penyimpanan Principal (User) dan Session di PostgreSQL Internal
type Repository interface {
	// Operasi Principal (User)
	CreateUser(ctx context.Context, user *User) error
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, userID string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	SetUserActiveStatus(ctx context.Context, userID string, isActive bool) error
	UpdateUserRole(ctx context.Context, userID string, role string) error

	// Operasi Siklus Hidup Sesi
	CreateSession(ctx context.Context, session *Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	TouchSession(ctx context.Context, sessionID string, lastActivityAt, newIdleExpiresAt time.Time) error
	RotateSessionToken(
		ctx context.Context,
		sessionID string,
		expectedCurrentHash string,
		newTokenHash string,
		graceExpiresAt time.Time,
		rotatedAt time.Time,
		newIdleExpiresAt time.Time,
	) (bool, error)
	RevokeByTokenHash(ctx context.Context, tokenHash string) error
	RevokeAllUserSessions(ctx context.Context, userID string) error
}

// ============================================================================
// 1. Implementasi PostgreSQL Internal (Production)
// ============================================================================

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateUser(ctx context.Context, user *User) error {
	email := user.Email
	if email == "" {
		if strings.Contains(user.Username, "@") {
			email = strings.ToLower(user.Username)
		} else {
			email = strings.ToLower(user.Username) + "@email.com"
		}
	}
	query := `
		INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (username) DO UPDATE
		SET email = EXCLUDED.email,
		    password_hash = EXCLUDED.password_hash,
		    role = EXCLUDED.role,
		    is_active = EXCLUDED.is_active,
		    updated_at = EXCLUDED.updated_at
	`
	_, err := r.db.ExecContext(ctx, query,
		user.ID,
		user.Username,
		email,
		user.PasswordHash,
		user.Role,
		user.IsActive,
		user.CreatedAt,
	)
	return err
}

func (r *PostgresRepository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, is_active, created_at
		FROM users
		WHERE username = $1
	`
	var u User
	err := r.db.QueryRowContext(ctx, query, username).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, is_active, created_at
		FROM users
		WHERE LOWER(email) = LOWER($1)
	`
	var u User
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PostgresRepository) GetUserByID(ctx context.Context, userID string) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, is_active, created_at
		FROM users
		WHERE id = $1
	`
	var u User
	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *PostgresRepository) ListUsers(ctx context.Context) ([]*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, is_active, created_at
		FROM users
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, &u)
	}
	return list, rows.Err()
}

func (r *PostgresRepository) SetUserActiveStatus(ctx context.Context, userID string, isActive bool) error {
	query := `UPDATE users SET is_active = $2, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, userID, isActive)
	return err
}

func (r *PostgresRepository) UpdateUserRole(ctx context.Context, userID string, role string) error {
	query := `UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, userID, role)
	return err
}

func (r *PostgresRepository) CreateSession(ctx context.Context, s *Session) error {
	query := `
		INSERT INTO sessions (
			id, user_id, token_hash, previous_token_hash, grace_period_expires_at,
			created_at, last_activity_at, last_rotated_at,
			idle_expires_at, absolute_expires_at, is_revoked
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	var prevHash sql.NullString
	if s.PreviousTokenHash != "" {
		prevHash = sql.NullString{String: s.PreviousTokenHash, Valid: true}
	}
	var graceExp sql.NullTime
	if s.GracePeriodExpiresAt != nil {
		graceExp = sql.NullTime{Time: *s.GracePeriodExpiresAt, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, query,
		s.ID, s.UserID, s.TokenHash, prevHash, graceExp,
		s.CreatedAt, s.LastActivityAt, s.LastRotatedAt,
		s.IdleExpiresAt, s.AbsoluteExpiresAt, s.IsRevoked,
	)
	return err
}

func (r *PostgresRepository) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	query := `
		SELECT
			id, user_id, token_hash, previous_token_hash, grace_period_expires_at,
			created_at, last_activity_at, last_rotated_at,
			idle_expires_at, absolute_expires_at, is_revoked
		FROM sessions
		WHERE token_hash = $1 OR previous_token_hash = $1
		LIMIT 1
	`
	var s Session
	var prevHash sql.NullString
	var graceExp sql.NullTime

	err := r.db.QueryRowContext(ctx, query, tokenHash).Scan(
		&s.ID, &s.UserID, &s.TokenHash, &prevHash, &graceExp,
		&s.CreatedAt, &s.LastActivityAt, &s.LastRotatedAt,
		&s.IdleExpiresAt, &s.AbsoluteExpiresAt, &s.IsRevoked,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if prevHash.Valid {
		s.PreviousTokenHash = prevHash.String
	}
	if graceExp.Valid {
		t := graceExp.Time
		s.GracePeriodExpiresAt = &t
	}
	return &s, nil
}

func (r *PostgresRepository) TouchSession(ctx context.Context, sessionID string, lastActivityAt, newIdleExpiresAt time.Time) error {
	query := `
		UPDATE sessions
		SET last_activity_at = $2,
		    idle_expires_at = $3
		WHERE id = $1 AND is_revoked = FALSE
	`
	_, err := r.db.ExecContext(ctx, query, sessionID, lastActivityAt, newIdleExpiresAt)
	return err
}

// RotateSessionToken merotasi session identifier secara atomik tanpa memperpanjang absolute_expires_at
func (r *PostgresRepository) RotateSessionToken(
	ctx context.Context,
	sessionID string,
	expectedCurrentHash string,
	newTokenHash string,
	graceExpiresAt time.Time,
	rotatedAt time.Time,
	newIdleExpiresAt time.Time,
) (bool, error) {
	query := `
		UPDATE sessions
		SET token_hash = $3,
		    previous_token_hash = $2,
		    grace_period_expires_at = $4,
		    last_rotated_at = $5,
		    last_activity_at = $5,
		    idle_expires_at = $6
		WHERE id = $1 AND token_hash = $2 AND is_revoked = FALSE
	`
	res, err := r.db.ExecContext(ctx, query,
		sessionID, expectedCurrentHash, newTokenHash, graceExpiresAt, rotatedAt, newIdleExpiresAt,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *PostgresRepository) RevokeByTokenHash(ctx context.Context, tokenHash string) error {
	query := `
		UPDATE sessions
		SET is_revoked = TRUE,
		    grace_period_expires_at = NULL
		WHERE token_hash = $1 OR previous_token_hash = $1
	`
	_, err := r.db.ExecContext(ctx, query, tokenHash)
	return err
}

func (r *PostgresRepository) RevokeAllUserSessions(ctx context.Context, userID string) error {
	query := `
		UPDATE sessions
		SET is_revoked = TRUE,
		    grace_period_expires_at = NULL
		WHERE user_id = $1
	`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// ============================================================================
// 2. Implementasi MemoryRepository (Untuk Unit Testing & Fallback Tanpa DB)
// ============================================================================

type MemoryRepository struct {
	mu       sync.RWMutex
	users    map[string]*User    // key: user.ID
	byName   map[string]string   // key: username -> user.ID
	byEmail  map[string]string   // key: lower(email) -> user.ID
	sessions map[string]*Session // key: session.ID
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		users:    make(map[string]*User),
		byName:   make(map[string]string),
		byEmail:  make(map[string]string),
		sessions: make(map[string]*Session),
	}
}

func (m *MemoryRepository) CreateUser(_ context.Context, user *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := *user
	if cp.Email == "" {
		if strings.Contains(cp.Username, "@") {
			cp.Email = strings.ToLower(cp.Username)
		} else {
			cp.Email = strings.ToLower(cp.Username) + "@email.com"
		}
	}
	m.users[cp.ID] = &cp
	m.byName[cp.Username] = cp.ID
	m.byEmail[strings.ToLower(cp.Email)] = cp.ID
	return nil
}

func (m *MemoryRepository) GetUserByUsername(_ context.Context, username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.byName[username]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	u, ok := m.users[id]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	cp := *u
	return &cp, nil
}

func (m *MemoryRepository) GetUserByEmail(_ context.Context, email string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.byEmail[strings.ToLower(email)]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	u, ok := m.users[id]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	cp := *u
	return &cp, nil
}

func (m *MemoryRepository) GetUserByID(_ context.Context, userID string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	cp := *u
	return &cp, nil
}

func (m *MemoryRepository) ListUsers(_ context.Context) ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*User, 0, len(m.users))
	for _, u := range m.users {
		cp := *u
		list = append(list, &cp)
	}
	return list, nil
}

func (m *MemoryRepository) SetUserActiveStatus(_ context.Context, userID string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return ErrInvalidCredentials
	}
	u.IsActive = isActive
	return nil
}

func (m *MemoryRepository) UpdateUserRole(_ context.Context, userID string, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return ErrInvalidCredentials
	}
	u.Role = role
	return nil
}

func (m *MemoryRepository) CreateSession(_ context.Context, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := *session
	if session.GracePeriodExpiresAt != nil {
		t := *session.GracePeriodExpiresAt
		cp.GracePeriodExpiresAt = &t
	}
	m.sessions[cp.ID] = &cp
	return nil
}

func (m *MemoryRepository) GetSessionByTokenHash(_ context.Context, tokenHash string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.sessions {
		if s.TokenHash == tokenHash || (s.PreviousTokenHash != "" && s.PreviousTokenHash == tokenHash) {
			cp := *s
			if s.GracePeriodExpiresAt != nil {
				t := *s.GracePeriodExpiresAt
				cp.GracePeriodExpiresAt = &t
			}
			return &cp, nil
		}
	}
	return nil, ErrSessionNotFound
}

func (m *MemoryRepository) TouchSession(_ context.Context, sessionID string, lastActivityAt, newIdleExpiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok || s.IsRevoked {
		return ErrSessionNotFound
	}
	s.LastActivityAt = lastActivityAt
	s.IdleExpiresAt = newIdleExpiresAt
	return nil
}

func (m *MemoryRepository) RotateSessionToken(
	_ context.Context,
	sessionID string,
	expectedCurrentHash string,
	newTokenHash string,
	graceExpiresAt time.Time,
	rotatedAt time.Time,
	newIdleExpiresAt time.Time,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok || s.IsRevoked {
		return false, nil
	}
	if s.TokenHash != expectedCurrentHash {
		return false, nil
	}
	s.PreviousTokenHash = expectedCurrentHash
	s.TokenHash = newTokenHash
	g := graceExpiresAt
	s.GracePeriodExpiresAt = &g
	s.LastRotatedAt = rotatedAt
	s.LastActivityAt = rotatedAt
	s.IdleExpiresAt = newIdleExpiresAt
	// Perhatikan: s.AbsoluteExpiresAt TIDAK PERNAH diubah!
	return true, nil
}

func (m *MemoryRepository) RevokeByTokenHash(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		if s.TokenHash == tokenHash || (s.PreviousTokenHash != "" && s.PreviousTokenHash == tokenHash) {
			s.IsRevoked = true
			s.GracePeriodExpiresAt = nil
		}
	}
	return nil
}

func (m *MemoryRepository) RevokeAllUserSessions(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		if s.UserID == userID {
			s.IsRevoked = true
			s.GracePeriodExpiresAt = nil
		}
	}
	return nil
}
