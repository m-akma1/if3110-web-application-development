package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"dbplatform/internal/tracing"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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

// StartSQL starts one SQL execution: it opens a span named "sql <kind>" (a child of the span
// in ctx) and logs sql.start inside it. Run the statement with the returned context, then call
// end with the row count (-1 when unknown) and the error: it logs sql.result and ends the span.
func StartSQL(ctx context.Context, l *slog.Logger, q SQLLog) (context.Context, func(rows int64, err error)) {
	ctx, span := tracing.Tracer().Start(ctx, "sql "+q.Kind,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(q.spanAttrs()...))
	l.InfoContext(ctx, "sql.start", q.attrs()...)
	start := time.Now()
	return ctx, func(rows int64, err error) {
		sqlResult(ctx, l, q, start, rows, err)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			if code := SQLState(err); code != "" {
				span.SetAttributes(attribute.String("db.response.status_code", code))
			}
		} else if rows >= 0 {
			span.SetAttributes(attribute.Int64("db.response.returned_rows", rows))
		}
		span.End()
	}
}

// spanAttrs describes the statement on its span. Parameter values are left out, as in the log.
func (q SQLLog) spanAttrs() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.operation", operation(q.Query)),
		attribute.String("db.statement", truncate(q.Query)),
		attribute.String("sql_kind", q.Kind),
		attribute.String("database_id", q.DatabaseID),
	}
	for _, a := range q.Extra {
		attrs = append(attrs, attribute.String(a.Key, a.Value.String()))
	}
	return attrs
}

// operation is the first keyword of the statement (SELECT, UPDATE, ...), after leading
// whitespace, comments and parentheses.
func operation(query string) string {
	s := query
	for {
		s = strings.TrimLeft(s, " \t\r\n(")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		}
		break
	}
	end := strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
	})
	if end < 0 {
		end = len(s)
	}
	return strings.ToUpper(s[:end])
}

// sqlResult logs sql.result. rows < 0 means the row count is unknown.
func sqlResult(ctx context.Context, l *slog.Logger, q SQLLog, start time.Time, rows int64, err error) {
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
