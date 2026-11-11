package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dbplatform/domain"
	"dbplatform/internal/authn"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"
)

func migrationErrorResponse(err error) (int, string) {
	var rejected *migration.RejectedError
	switch {
	case errors.Is(err, rbac.ErrForbidden):
		return http.StatusForbidden, accessDeniedMessage(err)
	case errors.As(err, &rejected):
		return http.StatusUnprocessableEntity, "Script ditolak sebelum dijalankan: " + rejected.Error()
	case errors.Is(err, migration.ErrTargetUnavailable):
		return http.StatusUnprocessableEntity, "Database target sedang tidak terhubung. Minta Platform Admin memeriksa koneksinya, lalu coba lagi."
	case errors.Is(err, migration.ErrNotApproved),
		errors.Is(err, migration.ErrNotPendingReview),
		errors.Is(err, migration.ErrNotLeader),
		errors.Is(err, migration.ErrAlreadyReviewed),
		errors.Is(err, migration.ErrInvalidName),
		errors.Is(err, migration.ErrEmptyScript),
		errors.Is(err, migration.ErrScriptTooLarge),
		errors.Is(err, migration.ErrNotDraft),
		errors.Is(err, migration.ErrTargetBusy),
		errors.Is(err, migration.ErrNoRollbackScript),
		errors.Is(err, migration.ErrCannotRollbackType),
		errors.Is(err, migration.ErrOnlySuccessCanRollback),
		errors.Is(err, migration.ErrOnlyRolledBackCanForward),
		errors.Is(err, migration.ErrRollbackNotLatest),
		errors.Is(err, migration.ErrForwardNotLinear),
		errors.Is(err, migration.ErrAlreadyForwarded),
		errors.Is(err, migration.ErrPendingRollbackExists),
		errors.Is(err, migration.ErrPendingForwardExists),
		errors.Is(err, migration.ErrImmutableRollbackScript):
		return http.StatusUnprocessableEntity, err.Error()
	default:
		// If error is descriptive validation or target error, return 422 with message
		msg := err.Error()
		if strings.Contains(msg, "irreversible") || strings.Contains(msg, "rollback") || strings.Contains(msg, "forward") {
			return http.StatusUnprocessableEntity, msg
		}
		return http.StatusInternalServerError, "Terjadi kesalahan pada server. Silakan coba lagi."
	}
}

func (s *Server) logMigrationError(user *authn.User, op string, err error) {
	if errors.Is(err, rbac.ErrForbidden) {
		return
	}
	s.logger.Printf("[MIGRATION] %s gagal user=%s: %v", op, user.ID, err)
}

func toMigrationView(m *migration.Migration) domain.MigrationView {
	approvals := make([]domain.MigrationApproval, 0, len(m.Approvals))
	for _, a := range m.Approvals {
		approvals = append(approvals, domain.MigrationApproval{
			ID:              a.ID,
			MigrationID:     a.MigrationID,
			GroupID:         a.GroupID,
			GroupName:       a.GroupName,
			LeaderID:        a.LeaderID,
			LeaderName:      a.LeaderName,
			Status:          a.Status,
			ReviewedAt:      a.ReviewedAt,
			Note:            a.Note,
			RejectionReason: a.RejectionReason,
		})
	}
	sqlUp := m.SQLUp
	if sqlUp == "" && m.SQL != "" {
		sqlUp = m.SQL
	}
	return domain.MigrationView{
		ID:                 m.ID,
		DatabaseID:         m.DatabaseID,
		DatabaseName:       m.DatabaseName,
		Version:            m.Version,
		Name:               m.Name,
		SQL:                m.SQL,
		SQLUp:              sqlUp,
		SQLDown:            m.SQLDown,
		IsIrreversible:     m.IsIrreversible,
		IrreversibleReason: m.IrreversibleReason,
		ExecutionType:      m.ExecutionType,
		RollbackTargetID:   m.RollbackTargetID,
		SourceMigrationID:  m.SourceMigrationID,
		CanRollback:        m.CanRollback,
		CanForward:         m.CanForward,
		Status:             string(m.Status),
		ReviewRequired:     m.ReviewRequired,
		CanApprove:         m.CanApprove,
		RejectionReason:    m.RejectionReason,
		Approvals:          approvals,
		CreatedByName:      orDash(m.CreatedByName),
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		ExecutedByName:     orDash(m.ExecutedByName),
		ExecutedAt:         m.ExecutedAt,
		DurationMs:         m.DurationMs,
		Error:              m.Error,
	}
}

func toMigrationViews(list []*migration.Migration) []domain.MigrationView {
	out := make([]domain.MigrationView, 0, len(list))
	for _, m := range list {
		out = append(out, toMigrationView(m))
	}
	return out
}

func toDryRunView(res *migration.RunResult) domain.DryRunView {
	v := domain.DryRunView{OK: res.OK, Statements: res.Statements, DurationMs: ms(res.Duration.Seconds())}
	if res.Err != nil {
		v.Error = res.Err.String()
	}
	for _, st := range res.Steps {
		sv := domain.DryRunStepView{Index: st.Index, SQL: st.SQL, Kind: string(st.Kind), DurationMs: ms(st.Duration.Seconds())}
		if st.Plan != nil {
			sv.PlanningMs, sv.ExecutionMs = st.Plan.PlanningTime, st.Plan.ExecutionTime
			for _, row := range st.Plan.Rows() {
				sv.Nodes = append(sv.Nodes, toPlanNodeView(row))
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

func toPlanNodeView(row migration.PlanRow) domain.PlanNodeView {
	n := row.Node
	title := n.NodeType
	if n.Operation != "" && n.NodeType == "ModifyTable" {
		title = n.Operation
	}
	if n.Strategy != "" && n.NodeType == "Aggregate" {
		title = n.Strategy + " " + title
	}
	if n.IndexName != "" {
		title += " using " + n.IndexName
	}
	if n.RelationName != "" {
		rel := n.RelationName
		if n.Schema != "" {
			rel = n.Schema + "." + rel
		}
		if n.Alias != "" && n.Alias != n.RelationName {
			rel += " " + n.Alias
		}
		title += " on " + rel
	}
	var conds []string
	for _, c := range []struct{ label, value string }{
		{"Index Cond", n.IndexCond}, {"Recheck Cond", n.RecheckCond}, {"Hash Cond", n.HashCond},
		{"Merge Cond", n.MergeCond}, {"Join Filter", n.JoinFilter}, {"Filter", n.Filter},
	} {
		if c.value != "" {
			conds = append(conds, fmt.Sprintf("%s: %s", c.label, c.value))
		}
	}
	return domain.PlanNodeView{
		Depth:         row.Depth,
		Title:         title,
		NodeType:      n.NodeType,
		JoinType:      n.JoinType,
		EstRows:       n.PlanRows,
		ActualRows:    n.ActualRows,
		Loops:         n.ActualLoops,
		StartupCost:   n.StartupCost,
		TotalCost:     n.TotalCost,
		ActualMs:      n.ActualTotalTime,
		Conditions:    conds,
		RowsRemoved:   n.RowsRemovedByFilter,
		SharedHit:     n.SharedHitBlocks,
		SharedRead:    n.SharedReadBlocks,
		NeverExecuted: n.NeverExecuted(),
		Misestimated:  n.Misestimated(),
	}
}

func ms(seconds float64) float64 { return seconds * 1000 }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
