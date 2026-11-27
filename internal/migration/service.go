package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"dbplatform/internal/logging"
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
	s := &Service{repo: repo, engine: engine, access: access, grants: g}
	if engine != nil {
		// The engine asks the service again, under the database lock, whether a run is allowed.
		engine.SetRevalidator(s.Revalidate)
	}
	return s
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
	if ov.History, err = s.repo.ListByDatabase(ctx, dbID, StatusQueued, StatusRunning, StatusSuccess, StatusFailed, StatusRolledBack, StatusPartial, StatusPartiallyReverted, StatusAborted); err != nil {
		return nil, err
	}
	latestActive, _ := s.repo.GetLatestActive(ctx, dbID)
	earliestFwd, _ := s.repo.GetEarliestUnforwardedRolledBack(ctx, dbID)
	for _, h := range ov.History {
		if latestActive != nil && h.ID == latestActive.ID && !h.IsIrreversible {
			if _, err := ResolveDownScript(h); err == nil {
				h.CanRollback = true
			}
		}
		if earliestFwd != nil && h.ID == earliestFwd.ID {
			h.CanForward = true
		}
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

// CreateDraftFull saves a new draft with dual scripts (up/down) and irreversible options.
func (s *Service) CreateDraftFull(ctx context.Context, in NewDraft) (*Migration, error) {
	if _, err := s.access.Authorize(ctx, in.CreatedBy, in.DatabaseID, rbac.ActionCreateMigration); err != nil {
		return nil, err
	}
	return s.repo.CreateDraft(ctx, in)
}

// SaveDraft changes a draft's name and script. Executed migrations return ErrNotDraft.
func (s *Service) SaveDraft(ctx context.Context, userID, id, name, script string) (*Migration, error) {
	if _, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration); err != nil {
		return nil, err
	}
	return s.repo.UpdateDraft(ctx, id, name, script)
}

// SaveDraftFull changes a draft's name, dual scripts (up/down), and irreversible options.
func (s *Service) SaveDraftFull(ctx context.Context, userID, id string, in UpdateDraftInput) (*Migration, error) {
	if _, _, err := s.load(ctx, userID, id, rbac.ActionCreateMigration); err != nil {
		return nil, err
	}
	return s.repo.UpdateDraftFull(ctx, id, in)
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
	in := NewDraft{DatabaseID: m.DatabaseID, Name: copyName(m.Name), SQL: m.SQL, CreatedBy: userID}
	if m.ExecutionType != "ROLLBACK" && m.SourceMigrationID == "" {
		// Copy the whole definition so a copied batch migration is still a batch migration.
		in.SQLUp, in.SQLDown = m.SQLUp, m.SQLDown
		in.IsIrreversible, in.IrreversibleReason = m.IsIrreversible, m.IrreversibleReason
		in.ExecutionMode, in.BatchAttribute, in.BatchSize = m.ExecutionMode, m.BatchAttribute, m.BatchSize
	}
	return s.repo.CreateDraft(ctx, in)
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

// checkExecutable applies the workflow rules before a run: not executed yet, not waiting for or
// rejected in review, and approved when groups have access to the database.
func (s *Service) checkExecutable(ctx context.Context, m *Migration) error {
	if !m.ExecutedAt.IsZero() || m.Status == StatusSuccess || m.Status == StatusFailed || m.Status == StatusRolledBack || m.Status == StatusAborted || m.Status == StatusPartial {
		return ErrNotDraft
	}
	if m.Status == StatusQueued || m.Status == StatusRunning {
		return ErrJobActive
	}
	if m.Status == StatusPendingReview || m.Status == StatusRejected {
		return ErrNotApproved
	}
	if err := ValidateReversibility(m); err != nil {
		return err
	}
	// Fail closed: when the groups with access cannot be read, the approval rule cannot be
	// checked, so nothing runs.
	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return err
	}
	if len(groupIDs) > 0 && m.Status != StatusApproved {
		return ErrNotApproved
	}
	return nil
}

// requireLeaders refuses to open a review that could never finish: every group in groupIDs
// must have an active leader to approve it.
func (s *Service) requireLeaders(ctx context.Context, groupIDs []string) error {
	names, err := s.repo.LeaderlessGroups(ctx, groupIDs)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("%w: %s", ErrGroupWithoutLeader, strings.Join(names, ", "))
	}
	return nil
}

// rollbackRequest validates a rollback of id. With groups on the database it creates the
// rollback draft for approval and returns it; otherwise it returns the migration to roll back
// and direct = true.
func (s *Service) rollbackRequest(ctx context.Context, userID, id string) (m *Migration, direct bool, err error) {
	m, _, err = s.load(ctx, userID, id, rbac.ActionExecuteMigration)
	if err != nil {
		return nil, false, err
	}
	downScript, err := validateRollbackTarget(ctx, s.repo.db, m)
	if err != nil {
		return nil, false, err
	}
	if pending, err := hasPendingActionFor(ctx, s.repo.db, m.ID, "ROLLBACK"); err != nil {
		return nil, false, err
	} else if pending {
		return nil, false, ErrPendingRollbackExists
	}
	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return nil, false, err
	}
	if len(groupIDs) == 0 {
		return m, true, nil
	}
	if err := s.requireLeaders(ctx, groupIDs); err != nil {
		return nil, false, err
	}
	rb, err := s.repo.CreateDraft(ctx, NewDraft{
		DatabaseID:       m.DatabaseID,
		Name:             rollbackName(m.Name),
		SQL:              downScript,
		SQLUp:            m.SQLUp,
		SQLDown:          downScript,
		ExecutionType:    "ROLLBACK",
		RollbackTargetID: m.ID,
		CreatedBy:        userID,
	})
	if err != nil {
		return nil, false, err
	}
	return s.withApprovals(ctx, rb, groupIDs)
}

// withApprovals opens one approval per group on a new rollback/forward draft.
func (s *Service) withApprovals(ctx context.Context, d *Migration, groupIDs []string) (*Migration, bool, error) {
	if err := s.repo.CreateApprovals(ctx, d.ID, groupIDs); err != nil {
		return nil, false, err
	}
	d, err := s.repo.Get(ctx, d.ID)
	if err != nil {
		return nil, false, err
	}
	d.Approvals, _ = s.repo.ListApprovals(ctx, d.ID)
	return d, false, nil
}

func (s *Service) groupsWithAccess(ctx context.Context, dbID string) ([]string, error) {
	if s.grants == nil {
		return nil, nil
	}
	return s.grants.GroupsWithAccess(ctx, dbID)
}

// forwardRequest is rollbackRequest for forward: an approval draft, or the rolled-back
// migration to re-apply with direct = true.
func (s *Service) forwardRequest(ctx context.Context, userID, id string) (m *Migration, direct bool, err error) {
	m, _, err = s.load(ctx, userID, id, rbac.ActionExecuteMigration)
	if err != nil {
		return nil, false, err
	}
	upScript, err := validateForwardSource(ctx, s.repo.db, m)
	if err != nil {
		return nil, false, err
	}
	if pending, err := hasPendingActionFor(ctx, s.repo.db, m.ID, "FORWARD"); err != nil {
		return nil, false, err
	} else if pending {
		return nil, false, ErrPendingForwardExists
	}
	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return nil, false, err
	}
	if len(groupIDs) == 0 {
		return m, true, nil
	}
	if err := s.requireLeaders(ctx, groupIDs); err != nil {
		return nil, false, err
	}
	fwd, err := s.repo.CreateDraft(ctx, NewDraft{
		DatabaseID:         m.DatabaseID,
		Name:               forwardName(m.Name),
		SQL:                upScript,
		SQLUp:              upScript,
		SQLDown:            m.SQLDown,
		IsIrreversible:     m.IsIrreversible,
		IrreversibleReason: m.IrreversibleReason,
		ExecutionType:      "FORWARD",
		SourceMigrationID:  m.ID,
		CreatedBy:          userID,
	})
	if err != nil {
		return nil, false, err
	}
	return s.withApprovals(ctx, fwd, groupIDs)
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
	if err := ValidateReversibility(m); err != nil {
		return nil, err
	}
	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if err := s.requireLeaders(ctx, groupIDs); err != nil {
		return nil, err
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
		return nil, ErrRejectReasonRequired
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
	if m.Status == StatusSuccess && m.ExecutionType != "ROLLBACK" && !m.IsIrreversible {
		if latest, err := s.repo.GetLatestActive(ctx, m.DatabaseID); err == nil && latest.ID == m.ID {
			if _, err := ResolveDownScript(m); err == nil {
				m.CanRollback = true
			}
		}
	}
	if m.Status == StatusRolledBack && m.ExecutionType != "ROLLBACK" {
		if earliest, err := s.repo.GetEarliestUnforwardedRolledBack(ctx, m.DatabaseID); err == nil && earliest.ID == m.ID {
			m.CanForward = true
		}
	}
	if m.IsBatch() {
		if req, err := s.repo.LatestBatchRequest(ctx, m.ID); err == nil {
			if req.Status == BatchRequestPending {
				if _, err := s.repo.FindPendingBatchGroupForLeader(ctx, req.ID, userID); err == nil {
					req.CanApprove = true
				}
			}
			m.BatchRequest = req
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

// EnqueueExecute queues a run of a draft or approved migration (including approved rollback and
// forward drafts) and returns the QUEUED migration and its job. The workflow rules are checked
// now, so the user gets an error immediately; the engine checks again when the job runs.
func (s *Service) EnqueueExecute(ctx context.Context, userID, id string) (*Migration, *Job, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionExecuteMigration)
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkExecutable(ctx, m); err != nil {
		return nil, nil, err
	}
	job, err := s.repo.EnqueueJob(ctx, EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: JobExecute, ExecutorID: userID,
		CorrelationID: correlationID(ctx), MarkQueued: true,
	})
	if err != nil {
		return nil, nil, err
	}
	m, err = s.repo.Get(ctx, m.ID)
	return m, job, err
}

// EnqueueRollback requests a rollback. With groups on the database it returns the rollback
// draft waiting for approval and a nil job; otherwise it queues a ROLLBACK job.
func (s *Service) EnqueueRollback(ctx context.Context, userID, id string) (*Migration, *Job, error) {
	m, direct, err := s.rollbackRequest(ctx, userID, id)
	if err != nil || !direct {
		return m, nil, err
	}
	job, err := s.repo.EnqueueJob(ctx, EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: JobRollback, ExecutorID: userID,
		CorrelationID: correlationID(ctx),
	})
	return m, job, err
}

// EnqueueForward is EnqueueRollback for forward.
func (s *Service) EnqueueForward(ctx context.Context, userID, id string) (*Migration, *Job, error) {
	m, direct, err := s.forwardRequest(ctx, userID, id)
	if err != nil || !direct {
		return m, nil, err
	}
	job, err := s.repo.EnqueueJob(ctx, EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: JobForward, ExecutorID: userID,
		CorrelationID: correlationID(ctx),
	})
	return m, job, err
}

// GetJob returns a job when the user may view migrations on its database.
// An unknown job is reported as forbidden, so job IDs of other databases cannot be probed.
func (s *Service) GetJob(ctx context.Context, userID, jobID string) (*Job, error) {
	j, err := s.repo.GetJob(ctx, jobID)
	if errors.Is(err, ErrNotFound) {
		return nil, rbac.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.access.Authorize(ctx, userID, j.DatabaseID, rbac.ActionViewMigration); err != nil {
		return nil, err
	}
	return j, nil
}

// Status returns a migration with its newest job (nil when it never had one).
func (s *Service) Status(ctx context.Context, userID, id string) (*Migration, *Job, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionViewMigration)
	if err != nil {
		return nil, nil, err
	}
	j, err := s.repo.LatestJobForMigration(ctx, m.ID)
	if errors.Is(err, ErrNotFound) {
		return m, nil, nil
	}
	return m, j, err
}

func correlationID(ctx context.Context) string {
	if info := logging.Info(ctx); info != nil {
		return info.CorrelationID
	}
	return ""
}

// Executions returns the execution attempts of a migration the user may view, oldest first.
func (s *Service) Executions(ctx context.Context, userID, id string) ([]Execution, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionViewMigration)
	if err != nil {
		return nil, err
	}
	return s.repo.ListExecutions(ctx, m.ID)
}

// RequestBatchAction resumes or aborts a PARTIAL batch migration (see BatchAction). Like any
// change to a shared database it needs one approval per group with access:
//
//   - no group has access: it runs now (a queued job, or ABORTED for BatchAbortMark);
//   - otherwise it opens a request for the leaders and returns it (job nil, request PENDING);
//   - once that request is APPROVED, calling this again with the same action runs it.
//
// The returned request is nil when nothing needed approval.
func (s *Service) RequestBatchAction(ctx context.Context, userID, id string, action BatchAction) (*Migration, *BatchRequest, *Job, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionExecuteMigration)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkBatchAction(m, action); err != nil {
		return nil, nil, nil, err
	}
	if action == BatchAbortRollback {
		batches, err := s.repo.ListBatches(ctx, m.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if !recoveryComplete(batches) {
			return nil, nil, nil, ErrRecoveryMissing
		}
	}
	open, err := s.repo.OpenBatchRequest(ctx, m.ID)
	switch {
	case err == nil && open.Status == BatchRequestPending:
		return m, open, nil, ErrBatchRequestPending
	case err == nil && open.Action == action:
		// APPROVED for this action: run it.
		m, job, err := s.runBatchAction(ctx, userID, m, action, open.ID)
		if err == nil {
			open, _ = s.repo.GetBatchRequest(ctx, open.ID)
		}
		return m, open, job, err
	case err != nil && !errors.Is(err, ErrNotFound):
		return nil, nil, nil, err
	}
	// No open request, or an approved one for another action, which CreateBatchRequest cancels.

	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(groupIDs) == 0 {
		m, job, err := s.runBatchAction(ctx, userID, m, action, "")
		return m, nil, job, err
	}
	if err := s.requireLeaders(ctx, groupIDs); err != nil {
		return nil, nil, nil, err
	}
	req, err := s.repo.CreateBatchRequest(ctx, m, action, userID, groupIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	m.BatchRequest = req
	return m, req, nil, nil
}

// checkBatchAction checks that m is in a state where action makes sense.
func checkBatchAction(m *Migration, action BatchAction) error {
	switch action {
	case BatchResume:
		switch {
		case m.Status == StatusPartiallyReverted:
			return ErrBatchReverting
		case m.Status != StatusPartial:
			return ErrNotPartial
		}
	case BatchAbortRollback, BatchAbortMark:
		if m.Status != StatusPartial && m.Status != StatusPartiallyReverted {
			return ErrNotPartial
		}
		if action == BatchAbortRollback && (m.IsIrreversible || strings.TrimSpace(m.SQLDown) == "") {
			return ErrNoRollbackScript
		}
	default:
		return fmt.Errorf("aksi batch tidak dikenal: %s", action)
	}
	return nil
}

// runBatchAction runs action now: a RESUME or ABORT job, or ABORTED for BatchAbortMark (job nil).
// requestID is the APPROVED request it fulfils ("" when no approval was needed).
func (s *Service) runBatchAction(ctx context.Context, userID string, m *Migration, action BatchAction, requestID string) (*Migration, *Job, error) {
	if action == BatchAbortMark {
		if err := s.repo.MarkAborted(ctx, m.ID, requestID, userID); err != nil {
			return nil, nil, err
		}
		// ABORTED is final: the before-images are no longer needed (best effort, logged).
		_ = s.engine.PurgeRecovery(ctx, m.DatabaseID, m.ID)
		m, err := s.repo.Get(ctx, m.ID)
		return m, nil, err
	}
	jobType := JobResume
	if action == BatchAbortRollback {
		jobType = JobAbort
	}
	job, err := s.repo.EnqueueJob(ctx, EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: jobType, ExecutorID: userID,
		CorrelationID: correlationID(ctx), MarkQueued: true, BatchRequestID: requestID,
	})
	if err != nil {
		return nil, nil, err
	}
	m, err = s.repo.Get(ctx, m.ID)
	return m, job, err
}

// ApproveBatchAction records a leader's approval of a batch action request. The request is
// APPROVED once every group approved; an editor then runs it with RequestBatchAction.
func (s *Service) ApproveBatchAction(ctx context.Context, userID, requestID, note string) (*BatchRequest, error) {
	return s.reviewBatchAction(ctx, userID, requestID, true, note)
}

// RejectBatchAction records a leader's rejection (reason required); the request is final.
func (s *Service) RejectBatchAction(ctx context.Context, userID, requestID, reason string) (*BatchRequest, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, ErrRejectReasonRequired
	}
	return s.reviewBatchAction(ctx, userID, requestID, false, reason)
}

func (s *Service) reviewBatchAction(ctx context.Context, userID, requestID string, approve bool, text string) (*BatchRequest, error) {
	req, err := s.repo.GetBatchRequest(ctx, requestID)
	if errors.Is(err, ErrNotFound) {
		return nil, rbac.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	// The leader must see the database, like for a migration review.
	if _, _, err := s.load(ctx, userID, req.MigrationID, rbac.ActionViewMigration); err != nil {
		return nil, err
	}
	if req.Status != BatchRequestPending {
		return req, ErrBatchRequestNotPending
	}
	groupID, err := s.repo.FindPendingBatchGroupForLeader(ctx, req.ID, userID)
	if err != nil {
		return req, err
	}
	if err := s.repo.ReviewBatchGroup(ctx, req.ID, groupID, userID, approve, text); err != nil {
		return req, err
	}
	return s.repo.GetBatchRequest(ctx, req.ID)
}

// ListPendingBatchRequests returns the batch action requests waiting for the user's review
// (every waiting request for a Platform Admin, who may only look).
func (s *Service) ListPendingBatchRequests(ctx context.Context, userID string, isPlatformAdmin bool) ([]*BatchRequest, error) {
	return s.repo.ListPendingBatchRequests(ctx, userID, isPlatformAdmin)
}

// Batches returns the committed batches of a migration the user may view.
func (s *Service) Batches(ctx context.Context, userID, id string) ([]Batch, error) {
	m, _, err := s.load(ctx, userID, id, rbac.ActionViewMigration)
	if err != nil {
		return nil, err
	}
	return s.repo.ListBatches(ctx, m.ID)
}
