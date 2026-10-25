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
}

func NewService(repo *Repository, engine *Engine, access Authorizer) *Service {
	return &Service{repo: repo, engine: engine, access: access}
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
	if ov.History, err = s.repo.ListByDatabase(ctx, dbID, StatusSuccess, StatusFailed); err != nil {
		return nil, err
	}
	if ov.CanEdit {
		if ov.Drafts, err = s.repo.ListByDatabase(ctx, dbID, StatusDraft); err != nil {
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
	if !m.IsDraft() {
		return m, nil, ErrNotDraft
	}
	res, err := s.engine.DryRun(ctx, m.DatabaseID, m.SQL)
	return m, res, err
}

// Execute runs a draft on its database (COMMIT or ROLLBACK) and records the outcome.
func (s *Service) Execute(ctx context.Context, userID, id string) (*Migration, *RunResult, error) {
	if _, _, err := s.load(ctx, userID, id, rbac.ActionExecuteMigration); err != nil {
		return nil, nil, err
	}
	return s.engine.Execute(ctx, id, userID)
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
	return m, level, nil
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
