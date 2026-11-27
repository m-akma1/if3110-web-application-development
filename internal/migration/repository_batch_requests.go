package migration

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"dbplatform/internal/ids"
)

// Batch action requests (table batch_action_requests): leader approval for resume and abort of
// a PARTIAL batch migration. See BatchAction.

const selectBatchRequest = `
	SELECT r.id, r.migration_id, COALESCE(m.name, ''), r.database_id, COALESCE(t.name, ''),
	       r.action, r.status, COALESCE(r.requested_by::text, ''), COALESCE(u.username, ''),
	       r.rejection_reason, COALESCE(r.job_id::text, ''), r.created_at, r.decided_at, r.done_at
	FROM batch_action_requests r
	JOIN migrations m ON m.id = r.migration_id
	LEFT JOIN target_databases t ON t.id = r.database_id
	LEFT JOIN users u ON u.id = r.requested_by`

func scanBatchRequest(row rowScanner) (*BatchRequest, error) {
	var b BatchRequest
	var action string
	var decided, done sql.NullTime
	err := row.Scan(&b.ID, &b.MigrationID, &b.MigrationName, &b.DatabaseID, &b.DatabaseName,
		&action, &b.Status, &b.RequestedBy, &b.RequestedByName,
		&b.RejectionReason, &b.JobID, &b.CreatedAt, &decided, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Action = BatchAction(action)
	if decided.Valid {
		b.DecidedAt = &decided.Time
	}
	if done.Valid {
		b.DoneAt = &done.Time
	}
	return &b, nil
}

// CreateBatchRequest opens a PENDING request for action on m with one PENDING approval per
// group. An APPROVED request for another action that never ran is cancelled in the same
// transaction. ErrBatchRequestPending means a request is already waiting for the leaders.
func (r *Repository) CreateBatchRequest(ctx context.Context, m *Migration, action BatchAction, userID string, groupIDs []string) (*BatchRequest, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE batch_action_requests SET status = 'CANCELLED', done_at = NOW()
		WHERE migration_id = $1 AND status = 'APPROVED' AND action <> $2`, m.ID, string(action)); err != nil {
		return nil, err
	}
	var requestedBy any
	if ids.IsValid(userID) {
		requestedBy = userID
	}
	id := ids.New()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO batch_action_requests (id, migration_id, database_id, action, requested_by)
		VALUES ($1, $2, $3, $4, $5)`, id, m.ID, m.DatabaseID, string(action), requestedBy)
	if isUniqueViolation(err) {
		return nil, ErrBatchRequestPending
	}
	if err != nil {
		return nil, err
	}
	for _, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO batch_action_approvals (id, request_id, group_id) VALUES ($1, $2, $3)`,
			ids.New(), id, gid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetBatchRequest(ctx, id)
}

// GetBatchRequest returns one request with its approvals.
func (r *Repository) GetBatchRequest(ctx context.Context, id string) (*BatchRequest, error) {
	if !ids.IsValid(id) {
		return nil, ErrNotFound
	}
	b, err := scanBatchRequest(r.db.QueryRowContext(ctx, selectBatchRequest+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, err
	}
	b.Approvals, err = r.ListBatchApprovals(ctx, b.ID)
	return b, err
}

// OpenBatchRequest returns the PENDING or APPROVED request of a migration, or ErrNotFound.
func (r *Repository) OpenBatchRequest(ctx context.Context, migrationID string) (*BatchRequest, error) {
	return r.batchRequestWhere(ctx, `r.migration_id = $1 AND r.status IN ('PENDING', 'APPROVED')`, migrationID)
}

// LatestBatchRequest returns the newest request of a migration, or ErrNotFound.
func (r *Repository) LatestBatchRequest(ctx context.Context, migrationID string) (*BatchRequest, error) {
	return r.batchRequestWhere(ctx, `r.migration_id = $1`, migrationID)
}

func (r *Repository) batchRequestWhere(ctx context.Context, where, migrationID string) (*BatchRequest, error) {
	if !ids.IsValid(migrationID) {
		return nil, ErrNotFound
	}
	b, err := scanBatchRequest(r.db.QueryRowContext(ctx,
		selectBatchRequest+` WHERE `+where+` ORDER BY r.created_at DESC LIMIT 1`, migrationID))
	if err != nil {
		return nil, err
	}
	b.Approvals, err = r.ListBatchApprovals(ctx, b.ID)
	return b, err
}

// ListBatchApprovals returns the approvals of a request. Approval.MigrationID holds the request ID.
func (r *Repository) ListBatchApprovals(ctx context.Context, requestID string) ([]Approval, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.request_id, a.group_id, COALESCE(g.name, ''),
		       COALESCE(a.leader_id::text, ''), COALESCE(u.username, ''),
		       a.status, a.reviewed_at, a.note, a.rejection_reason, a.created_at
		FROM batch_action_approvals a
		LEFT JOIN groups g ON g.id = a.group_id
		LEFT JOIN users u ON u.id = a.leader_id
		WHERE a.request_id = $1
		ORDER BY a.created_at, g.name`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Approval
	for rows.Next() {
		var a Approval
		var reviewed sql.NullTime
		if err := rows.Scan(&a.ID, &a.MigrationID, &a.GroupID, &a.GroupName, &a.LeaderID, &a.LeaderName,
			&a.Status, &reviewed, &a.Note, &a.RejectionReason, &a.CreatedAt); err != nil {
			return nil, err
		}
		if reviewed.Valid {
			a.ReviewedAt = &reviewed.Time
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// FindPendingBatchGroupForLeader returns a group led by userID whose approval of the request is
// still PENDING, or ErrNotLeader.
func (r *Repository) FindPendingBatchGroupForLeader(ctx context.Context, requestID, userID string) (string, error) {
	var groupID string
	err := r.db.QueryRowContext(ctx, `
		SELECT a.group_id FROM batch_action_approvals a JOIN groups g ON g.id = a.group_id
		WHERE a.request_id = $1 AND g.leader_id = $2 AND a.status = 'PENDING'
		ORDER BY g.name LIMIT 1`, requestID, userID).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotLeader
	}
	return groupID, err
}

// ReviewBatchGroup records one group's decision. Approving the last pending group makes the
// request APPROVED; one rejection makes it REJECTED.
func (r *Repository) ReviewBatchGroup(ctx context.Context, requestID, groupID, leaderID string, approve bool, text string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	status, noteCol := "APPROVED", "note"
	if !approve {
		status, noteCol = "REJECTED", "rejection_reason"
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE batch_action_approvals SET status = $4, leader_id = $3, `+noteCol+` = $5, reviewed_at = NOW()
		WHERE request_id = $1 AND group_id = $2 AND status = 'PENDING'`,
		requestID, groupID, leaderID, status, text)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAlreadyReviewed
	}
	if !approve {
		_, err = tx.ExecContext(ctx, `
			UPDATE batch_action_requests SET status = 'REJECTED', rejection_reason = $2, decided_at = NOW()
			WHERE id = $1 AND status = 'PENDING'`, requestID, text)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE batch_action_requests SET status = 'APPROVED', decided_at = NOW()
			WHERE id = $1 AND status = 'PENDING'
			  AND NOT EXISTS (SELECT 1 FROM batch_action_approvals WHERE request_id = $1 AND status <> 'APPROVED')`,
			requestID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ListPendingBatchRequests returns the PENDING requests a leader may review (every PENDING
// request for a Platform Admin, who may only look), newest first, with CanApprove set.
func (r *Repository) ListPendingBatchRequests(ctx context.Context, userID string, isPlatformAdmin bool) ([]*BatchRequest, error) {
	q := selectBatchRequest + ` WHERE r.status = 'PENDING'`
	args := []any{}
	if !isPlatformAdmin {
		q += ` AND EXISTS (SELECT 1 FROM batch_action_approvals a JOIN groups g ON g.id = a.group_id
		                   WHERE a.request_id = r.id AND g.leader_id = $1)`
		args = append(args, userID)
	}
	rows, err := r.db.QueryContext(ctx, q+` ORDER BY r.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	var list []*BatchRequest
	for rows.Next() {
		b, err := scanBatchRequest(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, b := range list {
		if b.Approvals, err = r.ListBatchApprovals(ctx, b.ID); err != nil {
			return nil, err
		}
		if _, err := r.FindPendingBatchGroupForLeader(ctx, b.ID, userID); err == nil {
			b.CanApprove = true
		}
	}
	return list, nil
}

// LeaderlessGroups returns the names of the groups among groupIDs without an active leader.
func (r *Repository) LeaderlessGroups(ctx context.Context, groupIDs []string) ([]string, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT g.name FROM groups g LEFT JOIN users u ON u.id = g.leader_id
		WHERE g.id = ANY($1::uuid[]) AND (u.id IS NULL OR NOT u.is_active)
		ORDER BY g.name`, "{"+strings.Join(groupIDs, ",")+"}")
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

// completeBatchRequest marks an APPROVED request DONE inside tx (with the job that runs it, if
// any). ErrBatchRequestNotApproved means it is not APPROVED any more (a concurrent run).
func completeBatchRequest(ctx context.Context, tx querier, requestID, jobID, executorID string) error {
	var job, executor any
	if jobID != "" {
		job = jobID
	}
	if ids.IsValid(executorID) {
		executor = executorID
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE batch_action_requests SET status = 'DONE', job_id = $2, executed_by = $3, done_at = NOW()
		WHERE id = $1 AND status = 'APPROVED'`, requestID, job, executor)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBatchRequestNotApproved
	}
	return nil
}
