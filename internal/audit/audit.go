// Package audit writes audit events: who did what to which migration, database or grant, and
// with what outcome. Events are JSON log records named "audit.<action>" on the structured
// logger, so they carry actor and correlation_id like every other record and can be filtered
// with: jq 'select(.event | startswith("audit."))'.
package audit

import (
	"context"
	"log/slog"
)

// Actions. Request actions are written by the HTTP layer when a user asks for something;
// execution actions are written when a run on the target has a recorded outcome.
const (
	Submit   = "submit"  // draft sent for review
	Approve  = "approve" // group leader approved
	Reject   = "reject"  // group leader rejected
	Enqueue  = "enqueue" // execute/rollback/forward requested (queued, or waiting for approval)
	Execute  = "execute" // execution outcome of a migration
	Rollback = "rollback"
	Forward  = "forward"
	Grant    = "grant" // group access level on a database changed
	Abort    = "abort" // a PARTIAL batch migration was marked ABORTED without rollback
)

// Outcomes.
const (
	Success   = "success"
	Failure   = "failure"
	Denied    = "denied"
	Uncertain = "uncertain"
	Partial   = "partial" // a batch run stopped with some batches committed
)

// Log writes one audit event. Failures and denials are warnings so they stand out.
func Log(ctx context.Context, l *slog.Logger, action, outcome string, attrs ...slog.Attr) {
	if l == nil {
		l = slog.Default()
	}
	level := slog.LevelInfo
	if outcome != Success {
		level = slog.LevelWarn
	}
	all := make([]slog.Attr, 0, len(attrs)+2)
	all = append(all, slog.String("action", action), slog.String("outcome", outcome))
	all = append(all, attrs...)
	l.LogAttrs(ctx, level, "audit."+action, all...)
}
