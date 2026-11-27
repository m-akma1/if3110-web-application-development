package groups

import (
	"context"
	"database/sql"
	"errors"

	"dbplatform/internal/ids"
)

// Leader rules. Every group that has access to a database must have an active leader, because
// every change to that database needs the approval of each such group's leader:
//
//   - the leader is an active member of the group (Public included: every user is a member);
//   - a non-system group's leader goes to its earliest active member when the leader leaves or
//     is deactivated; Public's goes to the earliest active Platform Admin;
//   - a group without an active leader cannot be given access to a database, and its leader
//     cannot be cleared while it has access.
var (
	ErrNoActiveLeader  = errors.New("Group belum punya Group Leader aktif. Tetapkan leader sebelum memberi akses database")
	ErrLeaderRequired  = errors.New("Group ini wajib memiliki Group Leader aktif karena memiliki akses database")
	ErrLeaderNotMember = errors.New("User bukan anggota group ini")
	ErrLeaderInactive  = errors.New("User nonaktif tidak dapat menjadi Group Leader")
)

// SetLeader makes userID the leader of groupID. userID must be an active member. An empty
// userID picks the default leader (see the package rules) and clears the leader when there is
// none, which is refused while the group has database access.
func (r *Repository) SetLeader(ctx context.Context, groupID, userID string) error {
	if !ids.IsValid(groupID) {
		return ErrGroupNotFound
	}
	if userID == "" {
		next, err := r.defaultLeader(ctx, groupID, "")
		if err != nil {
			return err
		}
		if next == "" {
			if has, err := r.hasGrants(ctx, groupID); err != nil {
				return err
			} else if has {
				return ErrLeaderRequired
			}
		}
		return r.setLeader(ctx, groupID, next)
	}
	if !ids.IsValid(userID) {
		return ErrLeaderNotMember
	}
	var isMember, isActive bool
	err := r.db.QueryRowContext(ctx, `
		SELECT TRUE, u.is_active
		FROM group_members gm JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = $1 AND gm.user_id = $2`, groupID, userID).Scan(&isMember, &isActive)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrLeaderNotMember
	case err != nil:
		return err
	case !isActive:
		return ErrLeaderInactive
	}
	return r.setLeader(ctx, groupID, userID)
}

// setLeader writes leader_id; "" clears it.
func (r *Repository) setLeader(ctx context.Context, groupID, userID string) error {
	var leader any
	if userID != "" {
		leader = userID
	}
	res, err := r.db.ExecContext(ctx, `UPDATE groups SET leader_id = $1 WHERE id = $2`, leader, groupID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// defaultLeader is who leads groupID when its leader has to be replaced: the earliest active
// member, or for Public the earliest active Platform Admin. except is never chosen (the user
// who is leaving). "" means nobody qualifies.
func (r *Repository) defaultLeader(ctx context.Context, groupID, except string) (string, error) {
	q := `
		SELECT gm.user_id FROM group_members gm JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id = $1 AND u.is_active AND gm.user_id::text <> $2
		ORDER BY gm.added_at, gm.user_id LIMIT 1`
	if groupID == PublicGroupID {
		q = `
			SELECT u.id FROM users u
			WHERE u.is_active AND u.role = 'PLATFORM_ADMIN' AND u.id::text <> $2 AND $1::uuid IS NOT NULL
			ORDER BY u.created_at, u.id LIMIT 1`
	}
	var id string
	err := r.db.QueryRowContext(ctx, q, groupID, except).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// HasActiveLeader reports whether groupID has a leader whose account is active.
func (r *Repository) HasActiveLeader(ctx context.Context, groupID string) (bool, error) {
	if !ids.IsValid(groupID) {
		return false, ErrGroupNotFound
	}
	var ok bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM groups g JOIN users u ON u.id = g.leader_id
		               WHERE g.id = $1 AND u.is_active)`, groupID).Scan(&ok)
	return ok, err
}

func (r *Repository) hasGrants(ctx context.Context, groupID string) (bool, error) {
	var has bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM group_db_grants WHERE group_id = $1 AND access_level <> 'NONE')`,
		groupID).Scan(&has)
	return has, err
}

// HandOverLeadership gives every group led by userID to its default leader (see the package
// rules). It runs when userID is deactivated. It returns the groups left without a leader.
func (r *Repository) HandOverLeadership(ctx context.Context, userID string) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM groups WHERE leader_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	var led []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		led = append(led, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var leaderless []Group
	for _, gid := range led {
		next, err := r.defaultLeader(ctx, gid, userID)
		if err != nil {
			return nil, err
		}
		if err := r.setLeader(ctx, gid, next); err != nil {
			return nil, err
		}
		if next == "" {
			if g, err := r.Get(ctx, gid); err == nil {
				leaderless = append(leaderless, *g)
			}
		}
	}
	return leaderless, nil
}

// EnsureLeaders gives every non-system group whose leader is missing or inactive its default
// leader (see the package rules). It runs at startup; Public is EnsurePublicLeader's job.
func (r *Repository) EnsureLeaders(ctx context.Context) error {
	rows, err := r.db.QueryContext(ctx, `
		SELECT g.id FROM groups g LEFT JOIN users u ON u.id = g.leader_id
		WHERE NOT g.is_system AND (u.id IS NULL OR NOT u.is_active)`)
	if err != nil {
		return err
	}
	var groupIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		groupIDs = append(groupIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		next, err := r.defaultLeader(ctx, gid, "")
		if err != nil {
			return err
		}
		if next != "" {
			if err := r.setLeader(ctx, gid, next); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnsurePublicLeader makes preferredUserID (the first bootstrap admin) the leader of Public
// when Public has no active leader. When that account is missing or inactive, the default
// leader (the earliest active Platform Admin) is used. It returns the new leader, or "" when
// Public already had one or nobody qualifies.
func (r *Repository) EnsurePublicLeader(ctx context.Context, preferredUserID string) (string, error) {
	if ok, err := r.HasActiveLeader(ctx, PublicGroupID); err != nil || ok {
		return "", err
	}
	if preferredUserID != "" {
		err := r.SetLeader(ctx, PublicGroupID, preferredUserID)
		if err == nil {
			return preferredUserID, nil
		}
		if !errors.Is(err, ErrLeaderNotMember) && !errors.Is(err, ErrLeaderInactive) {
			return "", err
		}
	}
	next, err := r.defaultLeader(ctx, PublicGroupID, "")
	if err != nil || next == "" {
		return "", err
	}
	return next, r.setLeader(ctx, PublicGroupID, next)
}

// LeaderlessWithGrants lists the groups that have access to a database but no active leader.
// Changes to those databases cannot be approved until an admin assigns one.
func (r *Repository) LeaderlessWithGrants(ctx context.Context) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, groupSelect+`
		WHERE EXISTS (SELECT 1 FROM group_db_grants gr WHERE gr.group_id = g.id AND gr.access_level <> 'NONE')
		  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = g.leader_id AND u.is_active)
		ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	return scanGroups(rows)
}
