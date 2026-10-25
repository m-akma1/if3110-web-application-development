package authn

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryRepository is an in-memory Repository for unit tests only.
// The application itself always uses PostgresRepository; there is no in-memory runtime mode.
type MemoryRepository struct {
	mu       sync.RWMutex
	users    map[string]*User    // key: user.ID
	byName   map[string]string   // key: username -> user.ID
	byEmail  map[string]string   // key: email -> user.ID
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
	cp.Username = NormalizeUsername(cp.Username)
	cp.Email = NormalizeEmail(cp.Email)
	if _, taken := m.byName[cp.Username]; taken {
		return ErrUsernameTaken
	}
	if cp.Email != "" {
		if _, taken := m.byEmail[cp.Email]; taken {
			return ErrEmailTaken
		}
	}
	m.users[cp.ID] = &cp
	m.byName[cp.Username] = cp.ID
	if cp.Email != "" {
		m.byEmail[cp.Email] = cp.ID
	}
	return nil
}

func (m *MemoryRepository) lookup(index map[string]string, key string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := index[key]
	if !ok || key == "" {
		return nil, ErrUserNotFound
	}
	u, ok := m.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *MemoryRepository) GetUserByUsername(_ context.Context, username string) (*User, error) {
	return m.lookup(m.byName, NormalizeUsername(username))
}

func (m *MemoryRepository) GetUserByEmail(_ context.Context, email string) (*User, error) {
	return m.lookup(m.byEmail, NormalizeEmail(email))
}

func (m *MemoryRepository) GetUserByID(_ context.Context, userID string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[userID]
	if !ok {
		return nil, ErrUserNotFound
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
	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
		return list[i].Username < list[j].Username
	})
	return list, nil
}

func (m *MemoryRepository) CountActiveAdmins(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	n := 0
	for _, u := range m.users {
		if u.Role == RolePlatformAdmin && u.IsActive {
			n++
		}
	}
	return n, nil
}

func (m *MemoryRepository) SetUserActiveStatus(_ context.Context, userID string, isActive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.IsActive = isActive
	return nil
}

func (m *MemoryRepository) UpdateUserRole(_ context.Context, userID string, role string) error {
	if err := ValidateRole(role); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[userID]
	if !ok {
		return ErrUserNotFound
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
	// AbsoluteExpiresAt is never changed by rotation.
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
