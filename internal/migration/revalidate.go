package migration

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"dbplatform/internal/ids"
	"dbplatform/internal/rbac"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrRevalidation means a queued run is no longer allowed when the worker takes it: the rules
// are checked again under the database lock, because grants and approvals can change while a
// job waits in the queue. Nothing is sent to the target. See RevalidationError.
var ErrRevalidation = errors.New("revalidasi sebelum eksekusi gagal")

// RevalidationError says why a run was refused under the lock. It unwraps to ErrRevalidation.
type RevalidationError struct {
	Reason string
	// MissingGroups are groups with access now that did not approve the run (they got access
	// after the approval). The worker sends a migration back to review with a PENDING approval
	// for each of them.
	MissingGroups []string
}

func (e *RevalidationError) Error() string { return ErrRevalidation.Error() + ": " + e.Reason }
func (e *RevalidationError) Unwrap() error { return ErrRevalidation }

// ApprovalSource says which approvals cover a run.
type ApprovalSource int

const (
	// ApprovalsOfMigration: the migration's own review (migration_approvals); also approved
	// rollback and forward requests, which are migrations of their own.
	ApprovalsOfMigration ApprovalSource = iota
	// ApprovalsOfBatchRequest: the batch action request whose job is running (resume, abort).
	ApprovalsOfBatchRequest
	// ApprovalsNone: a direct rollback or forward, queued without approval because no group had
	// access then.
	ApprovalsNone
)

// RunCheck is what a Revalidator gets: the migration as re-read under the lock.
type RunCheck struct {
	Migration  *Migration
	ExecutorID string
	Approvals  ApprovalSource
	JobID      string // the job being run ("" outside the queue)
}

// Revalidator checks, under the database lock and right before any SQL is sent, that a run is
// still allowed. An error stops the run.
type Revalidator func(ctx context.Context, c RunCheck) error

// SetRevalidator registers fn (NewService registers Service.Revalidate). Without one the engine
// only re-checks the migration's own state.
func (e *Engine) SetRevalidator(fn Revalidator) { e.revalidator = fn }

func (e *Engine) revalidate(ctx context.Context, m *Migration, executorID string, src ApprovalSource) error {
	if e.revalidator == nil {
		return nil
	}
	err := e.revalidator(ctx, RunCheck{Migration: m, ExecutorID: executorID, Approvals: src, JobID: runFrom(ctx).JobID})
	if err != nil {
		trace.SpanFromContext(ctx).AddEvent("migration.revalidation_failed", trace.WithAttributes(attribute.String("error", err.Error())))
		e.logger.WarnContext(ctx, "migration.revalidation_failed",
			slog.String("migration_id", m.ID), slog.String("database_id", m.DatabaseID),
			slog.String("executor_id", executorID), slog.String("error", err.Error()))
	}
	return err
}

// Revalidate is the Revalidator of the service. It re-checks the two things that can change
// while a job waits in the queue:
//   - the executor still has VIEW_EDIT (ActionExecuteMigration) on the database;
//   - every group that has access now approved the run. A group granted access after the
//     approval never reviewed the change, so the run waits for it.
//
// Groups that lost access do not matter: their approval is no longer needed.
func (s *Service) Revalidate(ctx context.Context, c RunCheck) error {
	m := c.Migration
	if _, err := s.access.Authorize(ctx, c.ExecutorID, m.DatabaseID, rbac.ActionExecuteMigration); err != nil {
		if errors.Is(err, rbac.ErrForbidden) {
			return &RevalidationError{Reason: "pengguna yang menjalankan migration ini tidak lagi punya akses VIEW_EDIT ke database"}
		}
		return err
	}
	groupIDs, err := s.groupsWithAccess(ctx, m.DatabaseID)
	if err != nil {
		return err // fail closed
	}
	if len(groupIDs) == 0 {
		return nil
	}
	approved, err := s.repo.approvedGroups(ctx, c)
	if err != nil {
		return err
	}
	var missing []string
	for _, g := range groupIDs {
		if !approved[g] {
			missing = append(missing, g)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names, err := s.repo.groupNames(ctx, missing)
	if err != nil {
		return err
	}
	return &RevalidationError{
		Reason:        "group berikut punya akses ke database ini tetapi belum menyetujui eksekusi ini: " + strings.Join(names, ", "),
		MissingGroups: missing,
	}
}

// approvedGroups returns the groups that approved the run described by c.
func (r *Repository) approvedGroups(ctx context.Context, c RunCheck) (map[string]bool, error) {
	var query string
	var arg string
	switch c.Approvals {
	case ApprovalsOfMigration:
		query, arg = `SELECT group_id FROM migration_approvals WHERE migration_id = $1 AND status = 'APPROVED'`, c.Migration.ID
	case ApprovalsOfBatchRequest:
		if !ids.IsValid(c.JobID) {
			return map[string]bool{}, nil // not run from an approved request
		}
		query, arg = `
			SELECT a.group_id FROM batch_action_approvals a
			JOIN batch_action_requests r ON r.id = a.request_id
			WHERE r.job_id = $1 AND a.status = 'APPROVED'`, c.JobID
	default:
		return map[string]bool{}, nil
	}
	rows, err := r.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out[g] = true
	}
	return out, rows.Err()
}

func (r *Repository) groupNames(ctx context.Context, groupIDs []string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM groups WHERE id = ANY($1::uuid[]) ORDER BY name`,
		"{"+strings.Join(groupIDs, ",")+"}")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// ReopenReview sends a QUEUED/RUNNING migration whose run failed revalidation back to review:
// a PENDING approval for each group in groupIDs (the ones that did not approve), and status
// PENDING_REVIEW. Approvals the other groups gave are kept.
func (r *Repository) ReopenReview(ctx context.Context, migrationID string, groupIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, g := range groupIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO migration_approvals (id, migration_id, group_id, status) VALUES ($1, $2, $3, 'PENDING')
			ON CONFLICT (migration_id, group_id) DO UPDATE SET
				status = 'PENDING', leader_id = NULL, note = '', rejection_reason = '', reviewed_at = NULL`,
			ids.New(), migrationID, g); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'PENDING_REVIEW', updated_at = NOW()
		WHERE id = $1 AND status IN ('QUEUED', 'RUNNING')`, migrationID); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncApprovals brings open reviews in line with the current grants, after a grant changed or
// a group was deleted:
//   - a group that lost access no longer blocks a review: its PENDING approval is dropped;
//   - a group that got access must review too: it gets a PENDING approval on reviews and
//     approved migrations that did not run yet, and on open batch action requests;
//   - the status follows: APPROVED when nothing is pending any more, PENDING_REVIEW (PENDING
//     for a request) when something is.
//
// Queued and running jobs are not touched here; Revalidate checks them under the lock.
func (s *Service) SyncApprovals(ctx context.Context) error {
	if s.grants == nil {
		return nil // approvals are turned off (tests)
	}
	return s.repo.SyncApprovals(ctx)
}

// hasAccess is true when group alias.group_id has a grant above NONE on database db.
func hasAccess(group, db string) string {
	return `EXISTS (SELECT 1 FROM group_db_grants gg WHERE gg.group_id = ` + group + ` AND gg.database_id = ` + db +
		` AND gg.access_level IN ('VIEW_ONLY', 'VIEW_EDIT'))`
}

// SyncApprovals is Service.SyncApprovals on the platform database, in one transaction.
func (r *Repository) SyncApprovals(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		// migration reviews
		`DELETE FROM migration_approvals a USING migrations m
		 WHERE a.migration_id = m.id AND m.status = 'PENDING_REVIEW' AND a.status = 'PENDING'
		   AND NOT ` + hasAccess("a.group_id", "m.database_id"),
		`INSERT INTO migration_approvals (id, migration_id, group_id, status)
		 SELECT gen_random_uuid(), m.id, g.group_id, 'PENDING'
		 FROM migrations m JOIN group_db_grants g ON g.database_id = m.database_id AND g.access_level IN ('VIEW_ONLY', 'VIEW_EDIT')
		 WHERE m.status IN ('PENDING_REVIEW', 'APPROVED')
		   AND NOT EXISTS (SELECT 1 FROM migration_approvals a WHERE a.migration_id = m.id AND a.group_id = g.group_id)
		 ON CONFLICT DO NOTHING`,
		`UPDATE migrations m SET status = 'PENDING_REVIEW', updated_at = NOW()
		 WHERE m.status = 'APPROVED' AND EXISTS (SELECT 1 FROM migration_approvals a WHERE a.migration_id = m.id AND a.status = 'PENDING')`,
		`UPDATE migrations m SET status = 'APPROVED', updated_at = NOW()
		 WHERE m.status = 'PENDING_REVIEW' AND NOT EXISTS (SELECT 1 FROM migration_approvals a WHERE a.migration_id = m.id AND a.status <> 'APPROVED')`,
		// batch action requests
		`DELETE FROM batch_action_approvals a USING batch_action_requests r
		 WHERE a.request_id = r.id AND r.status = 'PENDING' AND a.status = 'PENDING'
		   AND NOT ` + hasAccess("a.group_id", "r.database_id"),
		`INSERT INTO batch_action_approvals (id, request_id, group_id)
		 SELECT gen_random_uuid(), r.id, g.group_id
		 FROM batch_action_requests r JOIN group_db_grants g ON g.database_id = r.database_id AND g.access_level IN ('VIEW_ONLY', 'VIEW_EDIT')
		 WHERE r.status IN ('PENDING', 'APPROVED')
		   AND NOT EXISTS (SELECT 1 FROM batch_action_approvals a WHERE a.request_id = r.id AND a.group_id = g.group_id)
		 ON CONFLICT DO NOTHING`,
		`UPDATE batch_action_requests r SET status = 'PENDING', decided_at = NULL
		 WHERE r.status = 'APPROVED' AND EXISTS (SELECT 1 FROM batch_action_approvals a WHERE a.request_id = r.id AND a.status = 'PENDING')`,
		`UPDATE batch_action_requests r SET status = 'APPROVED', decided_at = NOW()
		 WHERE r.status = 'PENDING' AND NOT EXISTS (SELECT 1 FROM batch_action_approvals a WHERE a.request_id = r.id AND a.status <> 'APPROVED')`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
