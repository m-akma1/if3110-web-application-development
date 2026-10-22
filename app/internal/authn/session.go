package authn

import (
	"sync"

	"dbplatform/app/internal/rbac"
)

const (
	SessionCookieName = "__Host-sid"
	RolePlatformAdmin = "PLATFORM_ADMIN"
	RoleUser          = "USER"
)

// SessionStore menyimpan sesi aktif di sisi server (opaque session ID -> AuthLayoutUser)
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*AuthLayoutUser
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*AuthLayoutUser),
	}
}

func (s *SessionStore) Save(sessionID string, user *AuthLayoutUser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = user
}

func (s *SessionStore) Get(sessionID string) (*AuthLayoutUser, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.sessions[sessionID]
	return u, ok
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// BuildUserProfile membentuk profil AuthLayoutUser beserta daftar group dan database sesuai role
func BuildUserProfile(username string) *AuthLayoutUser {
	isAdmin := username == "admin" || username == "Moreno"
	role := RoleUser
	if isAdmin {
		role = RolePlatformAdmin
	}

	return &AuthLayoutUser{
		ID:              "usr-" + username,
		Username:        username,
		Role:            role,
		IsPlatformAdmin: isAdmin,
		Groups: []GroupSummary{
			{ID: "grp-1", Name: "Backend-Engineers"},
		},
		Databases: []DatabaseAccessSummary{
			{
				DatabaseID:      "db-1",
				DatabaseName:    "PostgreSQL Utama",
				DBMS:            "POSTGRESQL",
				EffectiveAccess: string(rbac.AccessViewEdit),
			},
			{
				DatabaseID:      "db-2",
				DatabaseName:    "MySQL Production",
				DBMS:            "MYSQL",
				EffectiveAccess: string(rbac.AccessViewOnly),
			},
		},
	}
}
