// Package admin implements the Platform Admin console: users, groups, grants, invite codes and
// target database connections. It orchestrates the other packages and owns the side effects of
// privilege changes, so handlers stay thin:
//
//   - privilege increase (promotion, joining a group, raised grant) -> the affected users' sessions
//     rotate on their next request;
//   - privilege removal (demotion, deactivation, password reset) -> their sessions are revoked;
//   - lowered grants and removed memberships need nothing extra: rbac reads grants on every request.
package admin

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/app/domain"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/rbac"
)

var (
	// ErrSelfLockout stops an admin from demoting or deactivating their own account.
	ErrSelfLockout = errors.New("anda tidak dapat menurunkan role atau menonaktifkan akun anda sendiri")
	// ErrInvalidLevel means the grant level is not NONE, VIEW_ONLY or VIEW_EDIT.
	ErrInvalidLevel = errors.New("tingkat akses tidak valid")
)

type Service struct {
	sessions *authn.SessionStore
	groups   *groups.Repository
	grants   *rbac.PostgresGrantStore
	targets  *dbmanager.Manager
}

func NewService(sessions *authn.SessionStore, groupRepo *groups.Repository, grants *rbac.PostgresGrantStore, targets *dbmanager.Manager) *Service {
	return &Service{sessions: sessions, groups: groupRepo, grants: grants, targets: targets}
}

// ---------- users ----------

// CreateUser registers a user (role USER or PLATFORM_ADMIN) and adds them to the given groups.
func (s *Service) CreateUser(ctx context.Context, in authn.RegisterInput, groupIDs []string) (*authn.User, error) {
	u, err := s.sessions.RegisterUser(ctx, in)
	if err != nil {
		return nil, err
	}
	for _, gid := range groupIDs {
		if gid == "" || gid == groups.PublicGroupID {
			continue
		}
		if err := s.groups.AddMember(ctx, gid, u.ID); err != nil {
			return u, fmt.Errorf("user dibuat, tetapi gagal menambahkan ke group: %w", err)
		}
	}
	return u, nil
}

// SetUserRole changes a user's platform role. Promotion rotates their sessions; demotion revokes them.
func (s *Service) SetUserRole(ctx context.Context, actorID, userID, role string) error {
	if err := authn.ValidateRole(role); err != nil {
		return err
	}
	if actorID == userID && role != authn.RolePlatformAdmin {
		return ErrSelfLockout
	}
	u, err := s.sessions.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Role == role {
		return nil
	}
	if err := s.sessions.UpdateUserRole(ctx, userID, role); err != nil {
		return err
	}
	if role == authn.RolePlatformAdmin {
		return s.sessions.ForceRotateUserSessions(ctx, userID)
	}
	return s.sessions.RevokeAllSessions(ctx, userID)
}

// SetUserActive deactivates (revoking every session) or reactivates a user.
func (s *Service) SetUserActive(ctx context.Context, actorID, userID string, active bool) error {
	if !active && actorID == userID {
		return ErrSelfLockout
	}
	if _, err := s.sessions.GetUserByID(ctx, userID); err != nil {
		return err
	}
	if active {
		return s.sessions.ReactivateUser(ctx, userID)
	}
	return s.sessions.RevokeUserAccess(ctx, userID)
}

// ResetPassword sets a new password and signs the user out everywhere.
func (s *Service) ResetPassword(ctx context.Context, userID, password string) error {
	if _, err := s.sessions.GetUserByID(ctx, userID); err != nil {
		return err
	}
	return s.sessions.SetPassword(ctx, userID, password)
}

// ---------- groups ----------

func (s *Service) CreateGroup(ctx context.Context, name, description string) (*groups.Group, error) {
	return s.groups.Create(ctx, name, description)
}

func (s *Service) RenameGroup(ctx context.Context, groupID, name, description string) error {
	return s.groups.Rename(ctx, groupID, name, description)
}

func (s *Service) DeleteGroup(ctx context.Context, groupID string) error {
	return s.groups.Delete(ctx, groupID)
}

// AddMember adds a user to a group; joining a group can only add access, so their sessions rotate.
func (s *Service) AddMember(ctx context.Context, groupID, userID string) error {
	if _, err := s.sessions.GetUserByID(ctx, userID); err != nil {
		return err
	}
	if _, err := s.groups.Get(ctx, groupID); err != nil {
		return err
	}
	if err := s.groups.AddMember(ctx, groupID, userID); err != nil {
		return err
	}
	return s.sessions.ForceRotateUserSessions(ctx, userID)
}

// RemoveMember removes a user from a group (not Public). Access drops on the user's next request.
func (s *Service) RemoveMember(ctx context.Context, groupID, userID string) error {
	return s.groups.RemoveMember(ctx, groupID, userID)
}

// ---------- grants ----------

// SetGrant sets a group's access level on a database. Raising it rotates every member's session.
func (s *Service) SetGrant(ctx context.Context, groupID, databaseID, level string) error {
	lvl := rbac.AccessLevel(level)
	if rbac.Normalize(lvl) != lvl {
		return ErrInvalidLevel
	}
	previous, err := s.grants.SetGroupGrant(ctx, groupID, databaseID, lvl)
	if err != nil {
		return err
	}
	if !rbac.Raises(previous, lvl) {
		return nil
	}
	members, err := s.groups.MemberIDs(ctx, groupID)
	if err != nil {
		return err
	}
	for _, uid := range members {
		if err := s.sessions.ForceRotateUserSessions(ctx, uid); err != nil {
			return err
		}
	}
	return nil
}

// ---------- invite codes ----------

func (s *Service) CreateInviteCode(ctx context.Context, actorID, groupID, code string) (*groups.InviteCode, error) {
	return s.groups.CreateInviteCode(ctx, code, groupID, actorID)
}

func (s *Service) SetInviteCodeActive(ctx context.Context, code string, active bool) error {
	return s.groups.SetInviteCodeActive(ctx, code, active)
}

// ---------- target databases ----------

func (s *Service) CreateTarget(ctx context.Context, in dbmanager.TargetInput) (*dbmanager.Target, error) {
	return s.targets.Create(ctx, in)
}

func (s *Service) UpdateTarget(ctx context.Context, id string, in dbmanager.TargetInput) (*dbmanager.Target, error) {
	return s.targets.Update(ctx, id, in)
}

func (s *Service) TestTarget(ctx context.Context, id string) (*dbmanager.Target, error) {
	return s.targets.Test(ctx, id)
}

func (s *Service) DeleteTarget(ctx context.Context, id string) error {
	return s.targets.Delete(ctx, id)
}

// ---------- overview ----------

// Overview loads everything the admin console displays.
func (s *Service) Overview(ctx context.Context) (*domain.AdminOverview, error) {
	users, err := s.sessions.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := s.groups.MemberGroupsByUser(ctx)
	if err != nil {
		return nil, err
	}
	allGroups, err := s.groups.List(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := s.targets.List(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := s.grants.ListGrants(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := s.groups.ListInviteCodes(ctx)
	if err != nil {
		return nil, err
	}

	ov := &domain.AdminOverview{}
	usernames := map[string]string{}
	for _, u := range users {
		usernames[u.ID] = u.Username
		item := domain.UserManagementItem{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, IsActive: u.IsActive}
		for _, g := range memberships[u.ID] {
			item.Groups = append(item.Groups, domain.GroupSummary{ID: g.ID, Name: g.Name, IsSystem: g.IsSystem})
		}
		ov.Users = append(ov.Users, item)
	}

	membersByGroup := map[string][]domain.MemberItem{}
	for _, u := range users {
		for _, g := range memberships[u.ID] {
			membersByGroup[g.ID] = append(membersByGroup[g.ID], domain.MemberItem{ID: u.ID, Username: u.Username})
		}
	}
	grantsByGroup := map[string]map[string]string{}
	for _, gr := range grants {
		if grantsByGroup[gr.GroupID] == nil {
			grantsByGroup[gr.GroupID] = map[string]string{}
		}
		grantsByGroup[gr.GroupID][gr.DatabaseID] = string(gr.Level)
	}
	for _, g := range allGroups {
		ov.Groups = append(ov.Groups, domain.GroupItem{
			ID: g.ID, Name: g.Name, Description: g.Description, IsSystem: g.IsSystem, MemberCount: g.MemberCount,
			Members: membersByGroup[g.ID], Grants: grantsByGroup[g.ID],
		})
	}

	for _, t := range targets {
		ov.Targets = append(ov.Targets, domain.TargetItem{
			ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, DBName: t.DBName, Username: t.Username,
			SSLMode: t.SSLMode, Status: t.Status, LastError: t.LastError, LastCheckedAt: t.LastCheckedAt,
		})
	}
	for _, c := range codes {
		ov.Codes = append(ov.Codes, domain.InviteCodeItem{Code: c.Code, GroupID: c.GroupID, GroupName: c.GroupName, IsActive: c.IsActive, CreatedAt: c.CreatedAt})
	}
	return ov, nil
}
