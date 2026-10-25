package authn

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"dbplatform/app/internal/rbac"
)

const (
	SessionCookieName    = "__Host-sid"
	RolePlatformAdmin    = "PLATFORM_ADMIN"
	RoleUser             = "USER"
	DefaultAdminPasscode = "OWNER-SECRET-2026"
)

var (
	ErrInvalidInviteCode = errors.New("kode undangan tim atau passcode admin tidak valid")
	teamCodesMu          sync.RWMutex
	defaultTeamCodes     = map[string]GroupSummary{
		"TEAM-BACKEND": {ID: "grp-1", Name: "Backend-Engineers"},
		"TEAM-ANALYST": {ID: "grp-2", Name: "Data-Analysts"},
	}
	userAccessMu       sync.RWMutex
	userDBAccessGrants = map[string]map[string]string{} // userID -> dbID -> EffectiveAccess
)

// RegisterTeamCode mendaftarkan kode undangan tim baru agar dapat digunakan saat Sign Up
func RegisterTeamCode(code, groupID, groupName string) {
	teamCodesMu.Lock()
	defer teamCodesMu.Unlock()
	defaultTeamCodes[strings.ToUpper(strings.TrimSpace(code))] = GroupSummary{
		ID:   groupID,
		Name: groupName,
	}
}

// SetUserDBAccess mengatur level akses efektif seorang user pada database tertentu
func SetUserDBAccess(userID, dbID, accessLevel string) {
	userAccessMu.Lock()
	defer userAccessMu.Unlock()
	if userDBAccessGrants[userID] == nil {
		userDBAccessGrants[userID] = make(map[string]string)
	}
	userDBAccessGrants[userID][dbID] = accessLevel
}

// GetUserDBAccess membaca level akses efektif seorang user pada database tertentu
func GetUserDBAccess(userID, dbID, fallback string) string {
	userAccessMu.RLock()
	defer userAccessMu.RUnlock()
	if m, ok := userDBAccessGrants[userID]; ok {
		if lvl, ok := m[dbID]; ok && lvl != "" {
			return lvl
		}
	}
	return fallback
}

// ValidateSignupCode memvalidasi pilihan tipe akun (Pemilik Database vs Anggota Tim) beserta kode khusus saat Sign Up
func ValidateSignupCode(accountType, inviteCode string) (string, GroupSummary, error) {
	code := strings.TrimSpace(inviteCode)
	if code == "" {
		return "", GroupSummary{}, ErrInvalidInviteCode
	}

	if strings.ToUpper(strings.TrimSpace(accountType)) == RolePlatformAdmin {
		expectedAdminCode := os.Getenv("ADMIN_REGISTRATION_CODE")
		if expectedAdminCode == "" {
			expectedAdminCode = DefaultAdminPasscode
		}
		if code != expectedAdminCode {
			return "", GroupSummary{}, ErrInvalidInviteCode
		}
		return RolePlatformAdmin, GroupSummary{ID: "grp-owner", Name: "Database-Owners"}, nil
	}

	teamCodesMu.RLock()
	grp, ok := defaultTeamCodes[strings.ToUpper(code)]
	teamCodesMu.RUnlock()
	if !ok {
		return "", GroupSummary{}, ErrInvalidInviteCode
	}
	return RoleUser, grp, nil
}

// SessionStore adalah mesin pengelola Identifikasi Principal & Siklus Hidup Sesi (Session Lifecycle Engine)
type SessionStore struct {
	repo    Repository
	cfg     SessionConfig
	nowFunc func() time.Time
}

// NewSessionStore membuat SessionStore dengan MemoryRepository yang sudah di-seed untuk pengujian/fallback
func NewSessionStore() *SessionStore {
	repo := NewMemoryRepository()
	s := NewSessionStoreWithRepo(repo, DefaultSessionConfig())
	_ = s.SeedDefaultUsers(context.Background())
	return s
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

// SeedDefaultUsers mengisi pengguna awal dengan password yang di-hash menggunakan Argon2id
func (s *SessionStore) SeedDefaultUsers(ctx context.Context) error {
	defaultAccounts := []struct {
		id       string
		username string
		email    string
		password string
		role     string
	}{
		{id: "usr-admin-001", username: "admin", email: "admin@email.com", password: "SuperSecretPass999!", role: RolePlatformAdmin},
		{id: "usr-moreno-002", username: "Moreno", email: "moreno@email.com", password: "Rahasia123", role: RolePlatformAdmin},
		{id: "usr-dhia-003", username: "dhia", email: "dhia@email.com", password: "PasswordDhia123!", role: RoleUser},
		{id: "usr-caca-004", username: "caca", email: "caca@email.com", password: "PasswordCaca123!", role: RoleUser},
	}

	now := s.nowFunc()
	for _, acc := range defaultAccounts {
		hash, err := HashPassword(acc.password)
		if err != nil {
			return err
		}
		u := &User{
			ID:           acc.id,
			Username:     acc.username,
			Email:        acc.email,
			PasswordHash: hash,
			Role:         acc.role,
			IsActive:     true,
			CreatedAt:    now,
		}
		if err := s.repo.CreateUser(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// RegisterUser mendaftarkan principal baru dengan password yang di-hash menggunakan Argon2id
func (s *SessionStore) RegisterUser(ctx context.Context, permanentID, username, plainPassword, role string) (*User, error) {
	email := ""
	if strings.Contains(username, "@") {
		email = strings.ToLower(username)
	} else {
		email = strings.ToLower(username) + "@email.com"
	}
	return s.RegisterUserWithEmail(ctx, permanentID, username, email, plainPassword, role)
}

// RegisterUserWithEmail mendaftarkan principal baru beserta alamat email dengan password yang di-hash menggunakan Argon2id
func (s *SessionStore) RegisterUserWithEmail(ctx context.Context, permanentID, username, email, plainPassword, role string) (*User, error) {
	hash, err := HashPassword(plainPassword)
	if err != nil {
		return nil, err
	}
	if permanentID == "" {
		tok, err := GenerateOpaqueToken()
		if err != nil {
			return nil, err
		}
		permanentID = "usr-" + tok[:12]
	}
	if role == "" {
		role = RoleUser
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if username == "" && email != "" {
		localPart := strings.SplitN(email, "@", 2)[0]
		if _, err := s.repo.GetUserByUsername(ctx, localPart); err != nil {
			username = localPart
		} else {
			username = email
		}
	}
	if email == "" && username != "" {
		if strings.Contains(username, "@") {
			email = strings.ToLower(username)
		} else {
			email = strings.ToLower(username) + "@email.com"
		}
	}
	user := &User{
		ID:           permanentID,
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

// UserExists memeriksa apakah username atau email sudah terdaftar
func (s *SessionStore) UserExists(ctx context.Context, identifier string) bool {
	if identifier == "" {
		return false
	}
	if _, err := s.repo.GetUserByEmail(ctx, identifier); err == nil {
		return true
	}
	_, err := s.repo.GetUserByUsername(ctx, identifier)
	return err == nil
}

// Authenticate memverifikasi email/username dan password menggunakan Argon2id, serta memuat unique identifier permanen pengguna
func (s *SessionStore) Authenticate(ctx context.Context, identifier, plainPassword string) (*User, *AuthLayoutUser, error) {
	if identifier == "" || plainPassword == "" {
		return nil, nil, ErrInvalidCredentials
	}

	var (
		user *User
		err  error
	)
	if strings.Contains(identifier, "@") {
		user, err = s.repo.GetUserByEmail(ctx, identifier)
		if err != nil {
			user, err = s.repo.GetUserByUsername(ctx, identifier)
		}
		if err != nil {
			localPart := strings.SplitN(identifier, "@", 2)[0]
			user, err = s.repo.GetUserByUsername(ctx, localPart)
			if err != nil && len(localPart) > 0 {
				titled := strings.ToUpper(localPart[:1]) + strings.ToLower(localPart[1:])
				user, err = s.repo.GetUserByUsername(ctx, titled)
			}
		}
	} else {
		user, err = s.repo.GetUserByUsername(ctx, identifier)
		if err != nil {
			user, err = s.repo.GetUserByEmail(ctx, identifier)
		}
	}
	if err != nil {
		// Jalankan dummy hash verification untuk mencegah timing attack enumerasi username/email
		VerifyDummyPassword(plainPassword)
		return nil, nil, ErrInvalidCredentials
	}

	ok, err := VerifyPassword(plainPassword, user.PasswordHash)
	if err != nil || !ok {
		return nil, nil, ErrInvalidCredentials
	}

	if !user.IsActive {
		return nil, nil, ErrUserInactive
	}

	return user, BuildUserProfileFromUser(user), nil
}

// Login melakukan autentikasi principal, mencabut sesi pra-login (rotasi saat login),
// dan menerbitkan opaque session identifier baru di PostgreSQL Internal.
func (s *SessionStore) Login(ctx context.Context, username, plainPassword, previousOpaqueToken string) (string, *AuthLayoutUser, error) {
	user, profile, err := s.Authenticate(ctx, username, plainPassword)
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

	return opaqueToken, profile, nil
}

// CreateSessionForUser menghasilkan opaque session identifier acak (CSPRNG) dan menyimpannya ke PostgreSQL Internal
func (s *SessionStore) CreateSessionForUser(ctx context.Context, userID string) (string, *Session, error) {
	rawToken, err := GenerateOpaqueToken()
	if err != nil {
		return "", nil, err
	}
	sessionID, err := GenerateOpaqueToken()
	if err != nil {
		return "", nil, err
	}

	now := s.nowFunc()
	absExp := now.Add(s.cfg.AbsoluteExpiration)
	idleExp := minTime(now.Add(s.cfg.IdleTimeout), absExp)

	session := &Session{
		ID:                "ses-" + sessionID[:16],
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

	profile := BuildUserProfileFromUser(user)
	newIdleExp := minTime(now.Add(s.cfg.IdleTimeout), sess.AbsoluteExpiresAt)

	// Jika request paralel masuk memakai token lama selama grace period:
	// Izinkan request tanpa merotasi ulang dan tanpa mengubah cookie baru yang sudah diterbitkan request utama
	if usedGraceSlot {
		return &ValidationResult{
			User:          profile,
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
				User:        profile,
				Session:     sess,
				Rotated:     true,
				NewOpaqueID: newRawToken,
			}, nil
		}

		// Jika rotated == false, berarti request paralel lain baru saja merotasi sesi ini beberapa milidetik lalu.
		// Request ini tetap sah dalam masa grace period.
		return &ValidationResult{
			User:          profile,
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
		User:    profile,
		Session: sess,
		Rotated: false,
	}, nil
}

// RotateOnPrivilegeChange merotasi session identifier seketika saat terjadi peningkatan/perubahan privilege
// dengan tetap mempertahankan grace period singkat untuk request paralel dan TIDAK memperpanjang absolute expiration.
func (s *SessionStore) RotateOnPrivilegeChange(ctx context.Context, rawOpaqueToken string, newRole string) (string, *AuthLayoutUser, error) {
	res, err := s.ValidateAndRefresh(ctx, rawOpaqueToken)
	if err != nil {
		return "", nil, err
	}

	if newRole != "" && newRole != res.User.Role {
		if err := s.repo.UpdateUserRole(ctx, res.User.ID, newRole); err != nil {
			return "", nil, err
		}
	}

	user, err := s.repo.GetUserByID(ctx, res.User.ID)
	if err != nil {
		return "", nil, err
	}
	profile := BuildUserProfileFromUser(user)

	// Jika ValidateAndRefresh di atas sudah kebetulan merotasi token, gunakan token baru tersebut
	if res.Rotated && res.NewOpaqueID != "" {
		return res.NewOpaqueID, profile, nil
	}

	now := s.nowFunc()
	newRawToken, err := GenerateOpaqueToken()
	if err != nil {
		return "", nil, err
	}
	newTokenHash := HashOpaqueToken(newRawToken)
	graceUntil := now.Add(s.cfg.GracePeriod)
	newIdleExp := minTime(now.Add(s.cfg.IdleTimeout), res.Session.AbsoluteExpiresAt)

	rotated, err := s.repo.RotateSessionToken(
		ctx,
		res.Session.ID,
		res.Session.TokenHash,
		newTokenHash,
		graceUntil,
		now,
		newIdleExp,
	)
	if err != nil {
		return "", nil, err
	}
	if !rotated {
		return "", nil, ErrSessionExpired
	}

	return newRawToken, profile, nil
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

// ListUsers mengembalikan seluruh pengguna yang terdaftar di platform untuk halaman Platform Admin
func (s *SessionStore) ListUsers(ctx context.Context) ([]*User, error) {
	return s.repo.ListUsers(ctx)
}

// UpdateUserRole mengubah peran pengguna (USER / PLATFORM_ADMIN)
func (s *SessionStore) UpdateUserRole(ctx context.Context, userID, role string) error {
	return s.repo.UpdateUserRole(ctx, userID, role)
}

// --- Helper Kompatibilitas untuk Pemanggilan Sederhana ---

func (s *SessionStore) Save(sessionID string, user *AuthLayoutUser) {
	if user == nil || sessionID == "" {
		return
	}
	now := s.nowFunc()
	_ = s.repo.CreateUser(context.Background(), &User{
		ID:           user.ID,
		Username:     user.Username,
		Email:        user.Email,
		PasswordHash: dummyPasswordHash,
		Role:         user.Role,
		IsActive:     true,
		CreatedAt:    now,
	})
	_ = s.repo.CreateSession(context.Background(), &Session{
		ID:                "ses-" + HashOpaqueToken(sessionID)[:16],
		UserID:            user.ID,
		TokenHash:         HashOpaqueToken(sessionID),
		CreatedAt:         now,
		LastActivityAt:    now,
		LastRotatedAt:     now,
		IdleExpiresAt:     now.Add(s.cfg.IdleTimeout),
		AbsoluteExpiresAt: now.Add(s.cfg.AbsoluteExpiration),
		IsRevoked:         false,
	})
}

func (s *SessionStore) Get(sessionID string) (*AuthLayoutUser, bool) {
	res, err := s.ValidateAndRefresh(context.Background(), sessionID)
	if err != nil || res == nil {
		return nil, false
	}
	return res.User, true
}

func (s *SessionStore) Delete(sessionID string) {
	_ = s.RevokeSession(context.Background(), sessionID)
}

// BuildUserProfileFromUser membentuk profil AuthLayoutUser dari entitas User permanen
func BuildUserProfileFromUser(u *User) *AuthLayoutUser {
	isAdmin := u.Role == RolePlatformAdmin
	defaultDB1 := string(rbac.AccessViewEdit)
	defaultDB2 := string(rbac.AccessViewOnly)
	if isAdmin {
		defaultDB2 = string(rbac.AccessViewEdit)
	}
	db1Access := GetUserDBAccess(u.ID, "db-1", defaultDB1)
	db2Access := GetUserDBAccess(u.ID, "db-2", defaultDB2)

	var dbs []DatabaseAccessSummary
	if db1Access != string(rbac.AccessNone) {
		dbs = append(dbs, DatabaseAccessSummary{
			DatabaseID:      "db-1",
			DatabaseName:    "PostgreSQL Utama",
			DBMS:            "POSTGRESQL",
			EffectiveAccess: db1Access,
		})
	}
	if db2Access != string(rbac.AccessNone) {
		dbs = append(dbs, DatabaseAccessSummary{
			DatabaseID:      "db-2",
			DatabaseName:    "MySQL Production",
			DBMS:            "MYSQL",
			EffectiveAccess: db2Access,
		})
	}

	return &AuthLayoutUser{
		ID:              u.ID,
		Username:        u.Username,
		Email:           u.Email,
		Role:            u.Role,
		IsPlatformAdmin: isAdmin,
		Groups: []GroupSummary{
			{ID: "grp-1", Name: "Backend-Engineers"},
		},
		Databases: dbs,
	}
}

// BuildUserProfile mempertahankan fungsi pembentuk profil berdasarkan username
func BuildUserProfile(username string) *AuthLayoutUser {
	isAdmin := username == "admin" || username == "Moreno"
	role := RoleUser
	if isAdmin {
		role = RolePlatformAdmin
	}
	return BuildUserProfileFromUser(&User{
		ID:       "usr-" + username,
		Username: username,
		Role:     role,
		IsActive: true,
	})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
