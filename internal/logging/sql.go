package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// MaxQueryLogLen bounds the SQL text written to one log record
const MaxQueryLogLen = 2000

// SQLLog describe one SQL execution for sql.start / sql.result records
type SQLLog struct {
	DatabaseID string
	Kind       string // read_only, catalog, dry_run, migration
	Query      string
	Params     []any
	Extra      []slog.Attr // e.g migration_id, execution_type
}

func truncate(s string) string {
	if len(s) <= MaxQueryLogLen {
		return s
	}
	cut := MaxQueryLogLen
	for cut > 0 && !utf8.RuneStart(s[cut]) { // never cut multi-byte character in half
		cut--
	}
	return s[:cut] + "...[truncated]"
}

// paramTypes log the type of each bind parameter, never is value
func paramTypes(params []any) []string {
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = fmt.Sprintf("%T", p)
	}
	return out
}

func (q SQLLog) attrs() []any {
	attrs := []any{
		slog.String("database_id", q.DatabaseID),
		slog.String("sql_kind", q.Kind),
		slog.String("query", truncate(q.Query)),
	}
	if len(q.Params) > 0 {
		attrs = append(attrs, slog.Any("params", paramTypes(q.Params)))
	}
	for _, a := range q.Extra {
		attrs = append(attrs, a)
	}
	return attrs
}

// SQLStart logs sql.start and returns the start time for sqlResult
func SQLStart(ctx context.Context, l *slog.Logger, q SQLLog) time.Time {
	l.InfoContext(ctx, "sql.start", q.attrs()...)
	return time.Now()
}

// SQLResult log sql.result. rows < 0 means the row count is unknown
func SQLResult(ctx context.Context, l *slog.Logger, q SQLLog, start time.Time, rows int64, err error) {
	attrs := append(q.attrs(), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
	if err != nil {
		attrs = append(attrs, slog.String("status", "FAILED"), slog.String("error", err.Error()))
		if code := SQLState(err); code != "" {
			attrs = append(attrs, slog.String("sqlstate", code))
		}
		l.ErrorContext(ctx, "sql.result", attrs...)
		return
	}

	attrs = append(attrs, slog.String("status", "SUCCESS"))
	if rows >= 0 {
		attrs = append(attrs, slog.Int64("rows", rows))
	}
	l.InfoContext(ctx, "sql.result", attrs...)
}

// SQLState returns the PostGreSQL error code wrapped in err, or ""
func SQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
