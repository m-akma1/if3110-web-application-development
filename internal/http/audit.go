package http

import (
	"context"
	"errors"
	"log/slog"

	"dbplatform/internal/audit"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"
)

// audit writes the audit.<action> event of a user request. The actor and correlation ID come
// from ctx (requireSession sets the actor). err decides the outcome: nil is success,
// rbac.ErrForbidden is denied, anything else is a failure with its message.
func (s *Server) audit(ctx context.Context, action string, err error, attrs ...slog.Attr) {
	outcome := audit.Success
	switch {
	case err == nil:
	case errors.Is(err, rbac.ErrForbidden):
		outcome = audit.Denied
	default:
		outcome = audit.Failure
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	audit.Log(ctx, s.logger, action, outcome, attrs...)
}

// migrationAttrs names the migration an audit event is about. m is nil when the request failed.
func migrationAttrs(id string, m *migration.Migration) []slog.Attr {
	attrs := []slog.Attr{slog.String("migration_id", id)}
	if m != nil {
		attrs = append(attrs, slog.String("database_id", m.DatabaseID), slog.String("status", string(m.Status)))
	}
	return attrs
}

// enqueueAttrs describes an execute/rollback/forward request: the job it queued, or the
// approval request it created instead.
func enqueueAttrs(request, id string, m *migration.Migration, job *migration.Job) []slog.Attr {
	attrs := append(migrationAttrs(id, m), slog.String("request", request))
	switch {
	case job != nil:
		attrs = append(attrs, slog.String("job_id", job.ID), slog.Bool("requires_approval", false))
	case m != nil:
		attrs = append(attrs, slog.Bool("requires_approval", true), slog.String("request_migration_id", m.ID))
	}
	return attrs
}
