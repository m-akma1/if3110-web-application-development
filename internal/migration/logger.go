package migration

import (
	"log/slog"
	"os"
	"time"
)

var attemptLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

func logMigrationAttempt(event, execType, migrationID, executionID, databaseID, executorID string, version int, status string, duration time.Duration, sqlScript string, res *RunResult) {
	attrs := []any{
		slog.String("event", event),
		slog.String("execution_type", execType),
		slog.String("migration_id", migrationID),
		slog.String("execution_id", executionID),
		slog.String("database_id", databaseID),
		slog.String("actor", executorID),
		slog.Int("version", version),
		slog.String("status", status),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.String("sql", sqlScript),
	}
	if res != nil && res.Err != nil {
		attrs = append(attrs,
			slog.String("error", res.Err.String()),
			slog.String("sqlstate", res.Err.Code),
		)
	}
	if status == "FAILED" {
		attemptLogger.Error("migration_attempt", attrs...)
	} else {
		attemptLogger.Info("migration_attempt", attrs...)
	}
}
