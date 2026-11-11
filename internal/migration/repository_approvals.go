package migration

import (
	"context"
	"database/sql"
	"errors"

	"dbplatform/internal/ids"
)

// CreateApprovals creates pending approval entries for groups and sets migration status to PENDING_REVIEW.
// If groupIDs is empty, the migration is auto-approved.
func (r *Repository) CreateApprovals(ctx context.Context, migrationID string, groupIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear any previous approvals
	if _, err := tx.ExecContext(ctx, `DELETE FROM migration_approvals WHERE migration_id = $1`, migrationID); err != nil {
		return err
	}

	if len(groupIDs) == 0 {
		// No groups with access require approval -> auto-approved
		if _, err := tx.ExecContext(ctx, `
			UPDATE migrations SET status = 'APPROVED', rejection_reason = '', updated_at = NOW()
			WHERE id = $1`, migrationID); err != nil {
			return err
		}
		return tx.Commit()
	}

	for _, gid := range groupIDs {
		aid := ids.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO migration_approvals (id, migration_id, group_id, status)
			VALUES ($1, $2, $3, 'PENDING')`, aid, migrationID, gid); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'PENDING_REVIEW', rejection_reason = '', updated_at = NOW()
		WHERE id = $1`, migrationID); err != nil {
		return err
	}

	return tx.Commit()
}

// ListApprovals returns all approval rows for a migration.
func (r *Repository) ListApprovals(ctx context.Context, migrationID string) ([]Approval, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.migration_id, a.group_id, COALESCE(g.name, ''),
		       COALESCE(a.leader_id::text, ''), COALESCE(u.username, ''),
		       a.status, a.reviewed_at, a.note, a.rejection_reason, a.created_at
		FROM migration_approvals a
		LEFT JOIN groups g ON g.id = a.group_id
		LEFT JOIN users u ON u.id = a.leader_id
		WHERE a.migration_id = $1
		ORDER BY a.created_at ASC`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Approval
	for rows.Next() {
		var ap Approval
		var reviewedAt sql.NullTime
		if err := rows.Scan(&ap.ID, &ap.MigrationID, &ap.GroupID, &ap.GroupName,
			&ap.LeaderID, &ap.LeaderName, &ap.Status, &reviewedAt, &ap.Note, &ap.RejectionReason, &ap.CreatedAt); err != nil {
			return nil, err
		}
		if reviewedAt.Valid {
			ap.ReviewedAt = &reviewedAt.Time
		}
		list = append(list, ap)
	}
	return list, rows.Err()
}

// ApproveGroup marks one group's approval as APPROVED. If all groups have approved, the migration status becomes APPROVED.
func (r *Repository) ApproveGroup(ctx context.Context, migrationID, groupID, leaderID, note string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE migration_approvals
		SET status = 'APPROVED', leader_id = $3, note = $4, reviewed_at = NOW()
		WHERE migration_id = $1 AND group_id = $2 AND status = 'PENDING'`,
		migrationID, groupID, leaderID, note)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrAlreadyReviewed
	}

	var pendingCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM migration_approvals
		WHERE migration_id = $1 AND status != 'APPROVED'`, migrationID).Scan(&pendingCount); err != nil {
		return false, err
	}

	allApproved := pendingCount == 0
	if allApproved {
		if _, err := tx.ExecContext(ctx, `
			UPDATE migrations SET status = 'APPROVED', updated_at = NOW()
			WHERE id = $1`, migrationID); err != nil {
			return false, err
		}
	}

	return allApproved, tx.Commit()
}

// RejectGroup marks one group's approval as REJECTED and sets the migration status to REJECTED.
func (r *Repository) RejectGroup(ctx context.Context, migrationID, groupID, leaderID, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE migration_approvals
		SET status = 'REJECTED', leader_id = $3, rejection_reason = $4, reviewed_at = NOW()
		WHERE migration_id = $1 AND group_id = $2 AND status = 'PENDING'`,
		migrationID, groupID, leaderID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAlreadyReviewed
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE migrations SET status = 'REJECTED', rejection_reason = $2, updated_at = NOW()
		WHERE id = $1`, migrationID, reason); err != nil {
		return err
	}

	return tx.Commit()
}

// FindPendingGroupForLeader finds a group that the user is leader of and has a PENDING approval on the migration.
func (r *Repository) FindPendingGroupForLeader(ctx context.Context, migrationID, userID string) (string, error) {
	var groupID string
	err := r.db.QueryRowContext(ctx, `
		SELECT a.group_id
		FROM migration_approvals a
		JOIN groups g ON g.id = a.group_id
		WHERE a.migration_id = $1 AND g.leader_id = $2 AND a.status = 'PENDING'
		LIMIT 1`, migrationID, userID).Scan(&groupID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotLeader
		}
		return "", err
	}
	return groupID, nil
}

// ListPendingReviews returns migrations in PENDING_REVIEW status.
// If isPlatformAdmin is true, it returns all pending migrations across the system.
// Otherwise, it returns migrations where a group led by userID is pending review.
func (r *Repository) ListPendingReviews(ctx context.Context, userID string, isPlatformAdmin bool) ([]*Migration, error) {
	var query string
	var args []any

	if isPlatformAdmin {
		query = selectMigrations + `
			WHERE m.status = 'PENDING_REVIEW'
			ORDER BY m.created_at DESC`
	} else {
		query = selectMigrations + `
			WHERE m.status = 'PENDING_REVIEW'
			  AND EXISTS (
				  SELECT 1 FROM migration_approvals a
				  JOIN groups g ON g.id = a.group_id
				  WHERE a.migration_id = m.id AND g.leader_id = $1
			  )
			ORDER BY m.created_at DESC`
		args = append(args, userID)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, m := range list {
		m.Approvals, _ = r.ListApprovals(ctx, m.ID)
		if _, err := r.FindPendingGroupForLeader(ctx, m.ID, userID); err == nil {
			m.CanApprove = true
		}
	}

	return list, nil
}
