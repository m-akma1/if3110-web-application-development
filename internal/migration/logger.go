package migration

import (
	"context"
	"log/slog"
	"time"
)

// logAttempt writes one migration.attempt record per execution (UP, ROLLBACK or FORWARD).
// The actor and correlation_id come from ctx. executionID is the history row of this run;
// it is empty when a failed rollback or forward wrote no row.
func (e *Engine) logAttempt(ctx context.Context, execType, migrationID, executionID, databaseID string,
	version int, status string, duration time.Duration, sqlScript string, res *RunResult) {
	attrs := []any{
		slog.String("execution_type", execType),
		slog.String("migration_id", migrationID),
		slog.String("execution_id", executionID),
		slog.String("database_id", databaseID),
		slog.Int("version", version),
		slog.String("status", status),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("query", sqlScript),
	}
	if res != nil && res.Err != nil {
		attrs = append(attrs, slog.String("error", res.Err.String()), slog.String("sqlstate", res.Err.Code))
	}
	if status == "FAILED" {
		e.logger.ErrorContext(ctx, "migration.attempt", attrs...)
	} else {
		e.logger.InfoContext(ctx, "migration.attempt", attrs...)
	}
}
