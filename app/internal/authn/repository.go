package authn

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"dbplatform/app/internal/ids"

	"github.com/jackc/pgx/v5/pgconn"
)

// Repository mengabstraksi penyimpanan Principal (User) dan Session di PostgreSQL Internal
type Repository interface {
	// Operasi Principal (User)
	CreateUser(ctx context.Context, user *User) error
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, userID string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	CountActiveAdmins(ctx context.Context) (int, error)
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

// PostgresRepository adalah implementasi Repository di atas PostgreSQL Internal (production).
// Skema tabelnya ada di internal/platformdb/migrations.
type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const userColumns = `id, username, email, password_hash, role, is_active, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var email sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &email, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.Email = email.String
	return &u, nil
}

// CreateUser menyisipkan user baru. Username atau email yang sudah dipakai menghasilkan
// ErrUsernameTaken / ErrEmailTaken; data yang sudah ada tidak pernah ditimpa.
func (r *PostgresRepository) CreateUser(ctx context.Context, user *User) error {
	var email sql.NullString
	if user.Email != "" {
		email = sql.NullString{String: user.Email, Valid: true}
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		user.ID, user.Username, email, user.PasswordHash, user.Role, user.IsActive, user.CreatedAt,
	)
	return mapUniqueViolation(err)
}

// mapUniqueViolation menerjemahkan pelanggaran unique index users menjadi error domain.
func mapUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_username_key":
			return ErrUsernameTaken
		case "users_email_key":
			return ErrEmailTaken
		}
	}
	return err
}

func (r *PostgresRepository) getUser(ctx context.Context, where string, arg any) (*User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return u, err
}

func (r *PostgresRepository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return r.getUser(ctx, `username = $1`, NormalizeUsername(username))
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, ErrUserNotFound
	}
	return r.getUser(ctx, `email = $1`, email)
}

func (r *PostgresRepository) GetUserByID(ctx context.Context, userID string) (*User, error) {
	if !ids.IsValid(userID) {
		return nil, ErrUserNotFound
	}
	return r.getUser(ctx, `id = $1`, userID)
}

func (r *PostgresRepository) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at ASC, username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

func (r *PostgresRepository) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = $1 AND is_active`, RolePlatformAdmin).Scan(&n)
	return n, err
}

func (r *PostgresRepository) SetUserActiveStatus(ctx context.Context, userID string, isActive bool) error {
	return r.updateUser(ctx, `UPDATE users SET is_active = $2, updated_at = NOW() WHERE id = $1`, userID, isActive)
}

func (r *PostgresRepository) UpdateUserRole(ctx context.Context, userID string, role string) error {
	if err := ValidateRole(role); err != nil {
		return err
	}
	return r.updateUser(ctx, `UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1`, userID, role)
}

func (r *PostgresRepository) updateUser(ctx context.Context, query, userID string, value any) error {
	if !ids.IsValid(userID) {
		return ErrUserNotFound
	}
	res, err := r.db.ExecContext(ctx, query, userID, value)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrUserNotFound
	}
	return nil
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

// RotateSessionToken merotasi session identifier secara atomik (compare-and-swap pada token_hash)
// tanpa memperpanjang absolute_expires_at.
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
	if !ids.IsValid(userID) {
		return nil
	}
	query := `
		UPDATE sessions
		SET is_revoked = TRUE,
		    grace_period_expires_at = NULL
		WHERE user_id = $1
	`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}
