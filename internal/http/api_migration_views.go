package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"dbplatform/domain"
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
	case errors.Is(err, migration.ErrJobActive),
		errors.Is(err, migration.ErrBatchRequestPending),
		errors.Is(err, migration.ErrBatchRequestNotApproved):
		return http.StatusConflict, err.Error()
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
		errors.Is(err, migration.ErrImmutableRollbackScript),
		errors.Is(err, migration.ErrHasHistory),
		errors.Is(err, migration.ErrBatchConfig),
		errors.Is(err, migration.ErrBatchScript),
		errors.Is(err, migration.ErrNotPartial),
		errors.Is(err, migration.ErrBatchReverting),
		errors.Is(err, migration.ErrGroupWithoutLeader),
		errors.Is(err, migration.ErrBatchRequestNotPending),
		errors.Is(err, migration.ErrRejectReasonRequired),
		errors.Is(err, migration.ErrSQLDownRequired),
		errors.Is(err, migration.ErrIrreversibleReason),
		errors.Is(err, migration.ErrBatchDeleteReversible),
		errors.Is(err, migration.ErrRecoveryMissing),
		errors.Is(err, migration.ErrRevalidation):
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

// logMigrationError logs a failed migration operation. Forbidden is skipped: authorizeDB
// already logs it as authz.denied. The actor comes from ctx (logging.SetActor).
func (s *Server) logMigrationError(ctx context.Context, op string, err error) {
	if errors.Is(err, rbac.ErrForbidden) {
		return
	}
	s.logger.ErrorContext(ctx, "migration.error",
		slog.String("operation", op),
		slog.String("error", err.Error()),
	)
}

func toApprovalViews(list []migration.Approval) []domain.MigrationApproval {
	out := make([]domain.MigrationApproval, 0, len(list))
	for _, a := range list {
		out = append(out, domain.MigrationApproval{
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
	return out
}

// toBatchRequestView is nil for a nil request.
func toBatchRequestView(b *migration.BatchRequest) *domain.BatchRequestView {
	if b == nil {
		return nil
	}
	return &domain.BatchRequestView{
		ID:              b.ID,
		MigrationID:     b.MigrationID,
		MigrationName:   b.MigrationName,
		DatabaseID:      b.DatabaseID,
		DatabaseName:    b.DatabaseName,
		Action:          string(b.Action),
		Status:          b.Status,
		RequestedByName: orDash(b.RequestedByName),
		RejectionReason: b.RejectionReason,
		JobID:           b.JobID,
		CreatedAt:       b.CreatedAt,
		DecidedAt:       b.DecidedAt,
		DoneAt:          b.DoneAt,
		Approvals:       toApprovalViews(b.Approvals),
		CanApprove:      b.CanApprove,
	}
}

func toBatchRequestViews(list []*migration.BatchRequest) []*domain.BatchRequestView {
	out := make([]*domain.BatchRequestView, 0, len(list))
	for _, b := range list {
		out = append(out, toBatchRequestView(b))
	}
	return out
}

func toMigrationView(m *migration.Migration) domain.MigrationView {
	approvals := toApprovalViews(m.Approvals)
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
		ExecutionMode:      m.ExecutionMode,
		BatchAttribute:     m.BatchAttribute,
		BatchSize:          m.BatchSize,
		BatchCommitted:     m.BatchesUp - m.BatchesDown,
		BatchReverted:      m.BatchesDown,
		BatchTotal:         m.BatchTotal,
		CheckpointCursor:   m.CheckpointCursor,
		BatchRequest:       toBatchRequestView(m.BatchRequest),
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

// toJobView converts a job for the API; nil stays nil (the migration never had a job).
func toJobView(j *migration.Job) *domain.MigrationJobView {
	if j == nil {
		return nil
	}
	return &domain.MigrationJobView{
		ID:          j.ID,
		MigrationID: j.MigrationID,
		DatabaseID:  j.DatabaseID,
		JobType:     string(j.Type),
		Status:      string(j.Status),
		Attempt:     j.Attempt,
		MaxAttempts: j.MaxAttempts,
		LastError:   j.LastError,
		NextRunAt:   j.NextRunAt,
		CreatedAt:   j.CreatedAt,
		FinishedAt:  j.FinishedAt,
		Done:        j.Done(),
	}
}

func toExecutionViews(list []migration.Execution) []domain.MigrationExecutionView {
	out := make([]domain.MigrationExecutionView, 0, len(list))
	for _, ex := range list {
		out = append(out, domain.MigrationExecutionView{
			ID:                 ex.ID,
			MigrationID:        ex.MigrationID,
			JobID:              ex.JobID,
			Attempt:            ex.Attempt,
			ExecutionType:      string(ex.Kind),
			Status:             string(ex.Status),
			SQLSent:            ex.SQLSent,
			SQLExecuted:        ex.SQL,
			RevertsExecutionID: ex.RevertsExecutionID,
			ExecutorName:       ex.ExecutorName,
			Error:              ex.Error,
			SQLState:           ex.SQLState,
			CorrelationID:      ex.CorrelationID,
			StartedAt:          ex.StartedAt,
			FinishedAt:         ex.FinishedAt,
			DurationMs:         ex.Duration.Milliseconds(),
		})
	}
	return out
}

func toBatchViews(list []migration.Batch) []domain.MigrationBatchView {
	out := make([]domain.MigrationBatchView, 0, len(list))
	for _, b := range list {
		out = append(out, domain.MigrationBatchView{
			Direction: b.Direction, BatchNo: b.No, ExecutionID: b.ExecutionID,
			CursorFrom: b.From, CursorTo: b.To, Rows: b.Rows, CommittedAt: b.CommittedAt,
			RecoveryRows: b.RecoveryRows,
		})
	}
	return out
}
