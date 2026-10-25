package authn

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"dbplatform/app/internal/ids"
)

const (
	SessionCookieName = "__Host-sid"
	RolePlatformAdmin = "PLATFORM_ADMIN"
	RoleUser          = "USER"
)

// SessionStore adalah mesin pengelola Identifikasi Principal & Siklus Hidup Sesi (Session Lifecycle Engine)
type SessionStore struct {
	repo    Repository
	cfg     SessionConfig
	nowFunc func() time.Time
}

// NewMemorySessionStore membuat SessionStore kosong di atas MemoryRepository (khusus unit test).
func NewMemorySessionStore() *SessionStore {
	return NewSessionStoreWithRepo(NewMemoryRepository(), DefaultSessionConfig())
}

// NewPostgresSessionStore membuat SessionStore yang terhubung langsung ke PostgreSQL Internal
func NewPostgresSessionStore(db *sql.DB, cfg SessionConfig) *SessionStore {
	repo := NewPostgresRepository(db)
	return NewSessionStoreWithRepo(repo, cfg)
}

// NewSessionStoreWithRepo membuat SessionStore dengan Repository dan SessionConfig kustom
func NewSessionStoreWithRepo(repo Repository, cfg SessionConfig) *SessionStore {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.AbsoluteExpiration <= 0 {
		cfg.AbsoluteExpiration = 12 * time.Hour
	}
	if cfg.RotationInterval <= 0 {
		cfg.RotationInterval = 15 * time.Minute
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 15 * time.Second
	}
	return &SessionStore{
		repo:    repo,
		cfg:     cfg,
		nowFunc: time.Now,
	}
}

// SetClock mengganti fungsi waktu (digunakan pada unit test untuk menyimulasikan waktu berjalan)
func (s *SessionStore) SetClock(clock func() time.Time) {
	if clock != nil {
		s.nowFunc = clock
	}
}

// RegisterInput adalah data pendaftaran principal baru.
type RegisterInput struct {
	Username string // wajib, unik (case-insensitive)
	Email    string // opsional, unik jika diisi
	Password string
	Role     string // kosong berarti RoleUser
}

// RegisterUser memvalidasi input lalu mendaftarkan principal baru dengan UUID permanen dan password Argon2id.
// Mengembalikan ErrInvalidUsername / ErrInvalidEmail / ErrWeakPassword / ErrUsernameTaken / ErrEmailTaken.
func (s *SessionStore) RegisterUser(ctx context.Context, in RegisterInput) (*User, error) {
	username := NormalizeUsername(in.Username)
	email := NormalizeEmail(in.Email)
	role := in.Role
	if role == "" {
		role = RoleUser
	}
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	if err := ValidateRole(role); err != nil {
		return nil, err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	user := &User{
		ID:           ids.New(),
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		IsActive:     true,
		CreatedAt:    s.nowFunc(),
	}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// GetUserByUsername memuat principal berdasarkan username (case-insensitive).
func (s *SessionStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return s.repo.GetUserByUsername(ctx, username)
}

// CountActiveAdmins menghitung Platform Admin yang masih aktif (dipakai saat bootstrap).
func (s *SessionStore) CountActiveAdmins(ctx context.Context) (int, error) {
	return s.repo.CountActiveAdmins(ctx)
}

// Authenticate memverifikasi username atau email beserta password (Argon2id), serta memuat UUID permanen pengguna
func (s *SessionStore) Authenticate(ctx context.Context, identifier, plainPassword string) (*User, error) {
	if identifier == "" || plainPassword == "" {
		return nil, ErrInvalidCredentials
	}

	// Input berisi '@' dicocokkan ke email, selain itu ke username. Tidak ada tebakan lain.
	var (
		user *User
		err  error
	)
	identifier = strings.TrimSpace(identifier)
	if strings.Contains(identifier, "@") {
		user, err = s.repo.GetUserByEmail(ctx, identifier)
	} else {
		user, err = s.repo.GetUserByUsername(ctx, identifier)
	}
	if err != nil {
		// Jalankan dummy hash verification untuk mencegah timing attack enumerasi username/email
		VerifyDummyPassword(plainPassword)
		return nil, ErrInvalidCredentials
	}

	ok, err := VerifyPassword(plainPassword, user.PasswordHash)
	if err != nil || !ok {
		return nil, ErrInvalidCredentials
	}

	if !user.IsActive {
		return nil, ErrUserInactive
	}

	return user, nil
}

// Login melakukan autentikasi principal, mencabut sesi pra-login (rotasi saat login),
// dan menerbitkan opaque session identifier baru di PostgreSQL Internal.
func (s *SessionStore) Login(ctx context.Context, username, plainPassword, previousOpaqueToken string) (string, *User, error) {
	user, err := s.Authenticate(ctx, username, plainPassword)
	if err != nil {
		return "", nil, err
	}

	// Rotasi saat login: jika klien mengirim session token lama sebelum login, cabut seketika
	if previousOpaqueToken != "" {
		_ = s.RevokeSession(ctx, previousOpaqueToken)
	}

	opaqueToken, _, err := s.CreateSessionForUser(ctx, user.ID)
	if err != nil {
		return "", nil, err
	}

	return opaqueToken, user, nil
}

// CreateSessionForUser menghasilkan opaque session identifier acak (CSPRNG) dan menyimpannya ke PostgreSQL Internal
func (s *SessionStore) CreateSessionForUser(ctx context.Context, userID string) (string, *Session, error) {
	rawToken, err := GenerateOpaqueToken()
	if err != nil {
		return "", nil, err
	}

	now := s.nowFunc()
	absExp := now.Add(s.cfg.AbsoluteExpiration)
	idleExp := minTime(now.Add(s.cfg.IdleTimeout), absExp)

	session := &Session{
		ID:                ids.New(),
		UserID:            userID,
		TokenHash:         HashOpaqueToken(rawToken),
		CreatedAt:         now,
		LastActivityAt:    now,
		LastRotatedAt:     now,
		IdleExpiresAt:     idleExp,
		AbsoluteExpiresAt: absExp,
		IsRevoked:         false,
	}

	if err := s.repo.CreateSession(ctx, session); err != nil {
		return "", nil, err
	}

	return rawToken, session, nil
}

// ValidateAndRefresh memeriksa idle timeout dan absolute expiration di setiap request,
// serta menangani rotasi berkala dengan grace period singkat untuk request paralel tanpa memperpanjang absolute expiration.
func (s *SessionStore) ValidateAndRefresh(ctx context.Context, rawOpaqueToken string) (*ValidationResult, error) {
	if rawOpaqueToken == "" {
		return nil, ErrSessionNotFound
	}

	now := s.nowFunc()
	incomingHash := HashOpaqueToken(rawOpaqueToken)

	sess, err := s.repo.GetSessionByTokenHash(ctx, incomingHash)
	if err != nil {
		return nil, ErrSessionNotFound
	}

	// cek apakah sesi telah dicabut (logout / revokasi akses)
	if sess.IsRevoked {
		return nil, ErrSessionRevoked
	}

	// cek absolute expiration (tidak pernah diperpanjang sejak login)
	if !now.Before(sess.AbsoluteExpiresAt) {
		_ = s.repo.RevokeByTokenHash(ctx, incomingHash)
		return nil, ErrSessionExpired
	}

	// cek idle Timeout
	if !now.Before(sess.IdleExpiresAt) {
		_ = s.repo.RevokeByTokenHash(ctx, incomingHash)
		return nil, ErrSessionExpired
	}

	// cek apakah request menggunakan identifier lama dalam masa Grace Period (request paralel)
	usedGraceSlot := false
	if sess.TokenHash != incomingHash {
		if sess.PreviousTokenHash == incomingHash && sess.GracePeriodExpiresAt != nil && !now.After(*sess.GracePeriodExpiresAt) {
			usedGraceSlot = true
		} else {
			return nil, ErrSessionExpired
		}
	}

	// muat Principal (User) berdasarkan unique identifier permanen (UserID) & pastikan masih aktif
	user, err := s.repo.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	if !user.IsActive {
		_ = s.repo.RevokeAllUserSessions(ctx, user.ID)
		return nil, ErrUserInactive
	}

	newIdleExp := minTime(now.Add(s.cfg.IdleTimeout), sess.AbsoluteExpiresAt)

	// Jika request paralel masuk memakai token lama selama grace period:
	// Izinkan request tanpa merotasi ulang dan tanpa mengubah cookie baru yang sudah diterbitkan request utama
	if usedGraceSlot {
		return &ValidationResult{
			User:          user,
			Session:       sess,
			Rotated:       false,
			UsedGraceSlot: true,
		}, nil
	}

	// cek apakah sesi memerlukan rotasi berkala (Periodic Rotation)
	if now.Sub(sess.LastRotatedAt) >= s.cfg.RotationInterval {
		newRawToken, err := GenerateOpaqueToken()
		if err != nil {
			return nil, err
		}
		newTokenHash := HashOpaqueToken(newRawToken)
		graceUntil := now.Add(s.cfg.GracePeriod)

		rotated, err := s.repo.RotateSessionToken(
			ctx,
			sess.ID,
			incomingHash,
			newTokenHash,
			graceUntil,
			now,
			newIdleExp,
		)
		if err != nil {
			return nil, err
		}

		if rotated {
			sess.PreviousTokenHash = incomingHash
			sess.TokenHash = newTokenHash
			sess.GracePeriodExpiresAt = &graceUntil
			sess.LastRotatedAt = now
			sess.LastActivityAt = now
			sess.IdleExpiresAt = newIdleExp

			return &ValidationResult{
				User:        user,
				Session:     sess,
				Rotated:     true,
				NewOpaqueID: newRawToken,
			}, nil
		}

		// Jika rotated == false, berarti request paralel lain baru saja merotasi sesi ini beberapa milidetik lalu.
		// Request ini tetap sah dalam masa grace period.
		return &ValidationResult{
			User:          user,
			Session:       sess,
			Rotated:       false,
			UsedGraceSlot: true,
		}, nil
	}

	// perbarui waktu aktivitas terakhir (idle timeout bergeser, tetapi absolute_expires_at tetap)
	if err := s.repo.TouchSession(ctx, sess.ID, now, newIdleExp); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return nil, err
	}
	sess.LastActivityAt = now
	sess.IdleExpiresAt = newIdleExp

	return &ValidationResult{
		User:    user,
		Session: sess,
		Rotated: false,
	}, nil
}

// RevokeSession menonaktifkan sesi seketika di backend saat pengguna logout
func (s *SessionStore) RevokeSession(ctx context.Context, rawOpaqueToken string) error {
	if rawOpaqueToken == "" {
		return nil
	}
	tokenHash := HashOpaqueToken(rawOpaqueToken)
	return s.repo.RevokeByTokenHash(ctx, tokenHash)
}

// RevokeUserAccess menonaktifkan akun pengguna dan mencabut seluruh sesi aktifnya seketika di backend
func (s *SessionStore) RevokeUserAccess(ctx context.Context, userID string) error {
	if err := s.repo.SetUserActiveStatus(ctx, userID, false); err != nil {
		return err
	}
	return s.repo.RevokeAllUserSessions(ctx, userID)
}

// GetUserByID memuat principal berdasarkan UUID permanen.
func (s *SessionStore) GetUserByID(ctx context.Context, userID string) (*User, error) {
	return s.repo.GetUserByID(ctx, userID)
}

// ForceRotateUserSessions membuat setiap sesi aktif milik user dirotasi pada request berikutnya.
// Dipakai saat privilege user meningkat (promosi role, masuk group, grant dinaikkan): nama cookie
// tetap, nilainya diganti, absolute expiration tidak berubah.
func (s *SessionStore) ForceRotateUserSessions(ctx context.Context, userID string) error {
	return s.repo.MarkSessionsForRotation(ctx, userID)
}

// RevokeAllSessions mencabut seluruh sesi user tanpa menonaktifkan akunnya (misal saat role diturunkan
// atau password direset), sehingga user wajib login ulang.
func (s *SessionStore) RevokeAllSessions(ctx context.Context, userID string) error {
	return s.repo.RevokeAllUserSessions(ctx, userID)
}

// ReactivateUser mengaktifkan kembali akun yang sebelumnya dinonaktifkan.
func (s *SessionStore) ReactivateUser(ctx context.Context, userID string) error {
	return s.repo.SetUserActiveStatus(ctx, userID, true)
}

// SetPassword mengganti password user (divalidasi, di-hash Argon2id) dan mencabut seluruh sesinya.
func (s *SessionStore) SetPassword(ctx context.Context, userID, plainPassword string) error {
	if err := ValidatePassword(plainPassword); err != nil {
		return err
	}
	hash, err := HashPassword(plainPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdatePasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	return s.repo.RevokeAllUserSessions(ctx, userID)
}

// ListUsers mengembalikan seluruh pengguna yang terdaftar di platform untuk halaman Platform Admin
func (s *SessionStore) ListUsers(ctx context.Context) ([]*User, error) {
	return s.repo.ListUsers(ctx)
}

// UpdateUserRole mengubah peran pengguna (USER / PLATFORM_ADMIN)
func (s *SessionStore) UpdateUserRole(ctx context.Context, userID, role string) error {
	return s.repo.UpdateUserRole(ctx, userID, role)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
