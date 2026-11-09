package migration

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"dbplatform/internal/rbac"
)

// Authorizer is the part of rbac.Authorizer the service needs.
type Authorizer interface {
	Authorize(ctx context.Context, userID, dbID string, action rbac.Action) (rbac.AccessLevel, error)
}

// GrantInspector inspects which groups have access grants on a target database.
type GrantInspector interface {
	GroupsWithAccess(ctx context.Context, databaseID string) ([]string, error)
}

// Service is what the HTTP layer calls. Every method authorizes the user through rbac first:
//   - VIEW_ONLY: history and detail of executed migrations.
//   - VIEW_EDIT: also drafts (see, create, edit, delete, duplicate), dry runs and execution.
//
// A migration the user may not see returns rbac.ErrForbidden, the same as a missing one,
// so ids cannot be probed.
type Service struct {
	repo   *Repository
	engine *Engine
	access Authorizer
	grants GrantInspector
}

func NewService(repo *Repository, engine *Engine, access Authorizer, grants ...GrantInspector) *Service {
	var g GrantInspector
	if len(grants) > 0 {
		g = grants[0]
	}
	return &Service{repo: repo, engine: engine, access: access, grants: g}
}

// Overview is the migration page of one database.
type Overview struct {
	DatabaseID string
	Level      rbac.AccessLevel
	CanEdit    bool         // VIEW_EDIT: may create, dry-run and execute
	Drafts     []*Migration // only filled when CanEdit
	History    []*Migration // SUCCESS and FAILED runs, newest first
}

// Overview lists the drafts (editors only) and the history of a database.
func (s *Service) Overview(ctx context.Context, userID, dbID string) (*Overview, error) {
	level, err := s.access.Authorize(ctx, userID, dbID, rbac.ActionViewMigration)
	if err != nil {
		return nil, err
	}
	ov := &Overview{DatabaseID: dbID, Level: level, CanEdit: rbac.IsAllowed(level, rbac.ActionCreateMigration)}
	if ov.History, err = s.repo.ListByDatabase(ctx, dbID, StatusSuccess, StatusFailed, StatusRolledBack, StatusPartial); err != nil {
		return nil, err
	}
	if ov.CanEdit {
		if ov.Drafts, err = s.repo.ListByDatabase(ctx, dbID, StatusDraft, StatusPendingReview, StatusApproved, StatusRejected); err != nil {
			return nil, err
		}
	}
	return ov, nil
}

// Get returns one migration and the user's level on its database.
// Drafts are work in progress and only visible to editors.
func (s *Service) Get(ctx context.Context, userID, id string) (*Migration, rbac.AccessLevel, error) {
	return s.load(ctx, userID, id, rbac.ActionViewMigration)
}

// CreateDraft saves a new draft on dbID.
func (s *Service) CreateDraft(ctx context.Context, userID, dbID, name, script string) (*Migration, error) {
	if _, err := s.access.Authorize(ctx, userID, dbID, rbac.ActionCreateMigration); err != nil {
		return nil, err
	}
	return s.repo.CreateDraft(ctx, NewDraft{DatabaseID: dbID, Name: name, SQL: script, CreatedBy: userID})
}

// SaveDraft changes a draft's name and script. Executed migrations return ErrNotDraft.
func (s *Service) SaveDraft(ctx context.Context, userID, id, name, script string) (*Migration, error) {
	if _, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration); err != nil {
		return nil, err
	}
	return s.repo.UpdateDraft(ctx, id, name, script)
}

// DeleteDraft removes a draft and returns it as it was.
func (s *Service) DeleteDraft(ctx context.Context, userID, id string) (*Migration, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration)
	if err != nil {
		return nil, err
	}
	return m, s.repo.DeleteDraft(ctx, id)
}

// Duplicate copies any migration (typically a FAILED one) into a new draft on the same database.
func (s *Service) Duplicate(ctx context.Context, userID, id string) (*Migration, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration)
	if err != nil {
		return nil, err
	}
	return s.repo.CreateDraft(ctx, NewDraft{DatabaseID: m.DatabaseID, Name: copyName(m.Name), SQL: m.SQL, CreatedBy: userID})
}

// DryRun runs a draft's script on its database and rolls it back, with a plan per DML statement.
func (s *Service) DryRun(ctx context.Context, userID, id string) (*Migration, *RunResult, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration)
	if err != nil {
		return nil, nil, err
	}
	if !m.IsDraft() && m.Status != StatusApproved {
		return m, nil, ErrNotDraft
	}
	res, err := s.engine.DryRun(ctx, m.DatabaseID, m.SQL)
	return m, res, err
}

// Execute runs a draft on its database (COMMIT or ROLLBACK) and records the outcome.
func (s *Service) Execute(ctx context.Context, userID, id string) (*Migration, *RunResult, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionExecuteMigration)
	if err != nil {
		return nil, nil, err
	}
	if !m.ExecutedAt.IsZero() || m.Status == StatusSuccess || m.Status == StatusFailed || m.Status == StatusRolledBack || m.Status == StatusAborted || m.Status == StatusPartial {
		return nil, nil, ErrNotDraft
	}
	if m.Status == StatusPendingReview || m.Status == StatusRejected {
		return nil, nil, ErrNotApproved
	}
	if s.grants != nil {
		groupIDs, err := s.grants.GroupsWithAccess(ctx, m.DatabaseID)
		if err == nil && len(groupIDs) > 0 && m.Status != StatusApproved {
			return nil, nil, ErrNotApproved
		}
	}
	return s.engine.Execute(ctx, id, userID)
}

// SubmitReview submits a draft or rejected migration for review by group leaders.
func (s *Service) SubmitReview(ctx context.Context, userID, id string) (*Migration, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration)
	if err != nil {
		return nil, err
	}
	if !m.IsDraft() {
		return nil, ErrNotDraft
	}
	var groupIDs []string
	if s.grants != nil {
		groupIDs, err = s.grants.GroupsWithAccess(ctx, m.DatabaseID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.repo.CreateApprovals(ctx, id, groupIDs); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

// Approve records a group leader's approval for the migration.
func (s *Service) Approve(ctx context.Context, userID, id, note string) (*Migration, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionViewMigration)
	if err != nil {
		return nil, err
	}
	if m.Status != StatusPendingReview {
		return nil, ErrNotPendingReview
	}
	groupID, err := s.repo.FindPendingGroupForLeader(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	_, err = s.repo.ApproveGroup(ctx, id, groupID, userID, note)
	if err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

// Reject records a group leader's rejection for the migration.
func (s *Service) Reject(ctx context.Context, userID, id, reason string) (*Migration, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("alasan penolakan wajib diisi")
	}
	m, _, err := s.load(ctx, userID, id, rbac.ActionViewMigration)
	if err != nil {
		return nil, err
	}
	if m.Status != StatusPendingReview {
		return nil, ErrNotPendingReview
	}
	groupID, err := s.repo.FindPendingGroupForLeader(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.RejectGroup(ctx, id, groupID, userID, reason); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, id)
}

// load fetches a migration and authorizes action on its database.
func (s *Service) load(ctx context.Context, userID, id string, action rbac.Action) (*Migration, rbac.AccessLevel, error) {
	m, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, rbac.AccessNone, rbac.ErrForbidden
	}
	if err != nil {
		return nil, rbac.AccessNone, err
	}
	if m.IsDraft() && action == rbac.ActionViewMigration {
		action = rbac.ActionCreateMigration
	}
	level, err := s.access.Authorize(ctx, userID, m.DatabaseID, action)
	if err != nil {
		return nil, level, err
	}

	m.Approvals, _ = s.repo.ListApprovals(ctx, m.ID)
	if m.Status == StatusPendingReview {
		if _, err := s.repo.FindPendingGroupForLeader(ctx, m.ID, userID); err == nil {
			m.CanApprove = true
		}
	}

	return m, level, nil
}

// ListPendingReviews returns migrations that are pending review for the user or platform admin.
func (s *Service) ListPendingReviews(ctx context.Context, userID string, isPlatformAdmin bool) ([]*Migration, error) {
	return s.repo.ListPendingReviews(ctx, userID, isPlatformAdmin)
}

const copySuffix = " (salinan)"

// copyName appends copySuffix, trimming the name so the result stays within MaxNameLength.
func copyName(name string) string {
	name = strings.TrimSpace(name)
	limit := MaxNameLength - utf8.RuneCountInString(copySuffix)
	if r := []rune(name); len(r) > limit {
		name = strings.TrimSpace(string(r[:limit]))
	}
	return name + copySuffix
}
