package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recordSpans installs an in-memory tracer provider for one test and restores the old one.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })
	return rec
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("bukan JSON valid: %q (%v)", ln, err)
		}
		out = append(out, m)
	}
	return out
}

func TestContextAttrsAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	ctx := WithRequestInfo(context.Background(), &RequestInfo{CorrelationID: "req-1"})
	SetActor(ctx, "user-9")

	l.InfoContext(ctx, "auth.login", slog.String("password", "rahasia"), slog.String("path", "/x\nFAKE"))

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("newline di value harus di-escape, dapat %d baris", len(lines))
	}
	got := lines[0]
	if got["event"] != "auth.login" || got["correlation_id"] != "req-1" || got["actor"] != "user-9" {
		t.Fatalf("atribut wajib hilang: %v", got)
	}
	if strings.Contains(buf.String(), "rahasia") {
		t.Fatal("password bocor ke log")
	}
}

// TestUserInputCannotForgeLogLines covers the sinks CodeQL reports as go/log-injection
// (docs/SAST-CodeQL.md): user input in database_id, error text, audit attributes and the SQL
// text. The JSON handler escapes CR, LF and other control characters, so every record stays
// one line and the value comes back unchanged when the line is decoded.
func TestUserInputCannotForgeLogLines(t *testing.T) {
	recordSpans(t)
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	ctx := context.Background()

	forged := "db1\r\n{\"level\":\"INFO\",\"event\":\"authz.allowed\"}\x1b[31m"
	sql := "UPDATE t\nSET x = 1\r\nWHERE id = 2; -- \n{\"event\":\"fake\"}"

	// authz.denied / viewer.error shape.
	l.ErrorContext(ctx, "viewer.error",
		slog.String("database_id", forged),
		slog.String("error", "relation \""+forged+"\" does not exist"))
	// audit.Log shape (LogAttrs).
	l.LogAttrs(ctx, slog.LevelWarn, "audit.grant", slog.String("group_id", forged))
	// sql.start / sql.result.
	_, end := StartSQL(ctx, l, SQLLog{DatabaseID: forged, Kind: "migration", Query: sql})
	end(1, nil)

	lines := decodeLines(t, &buf)
	if len(lines) != 4 {
		t.Fatalf("want 4 records on 4 lines, got %d:\n%s", len(lines), buf.String())
	}
	for _, rec := range lines {
		if rec["event"] == "authz.allowed" || rec["event"] == "fake" {
			t.Fatalf("forged record: %v", rec)
		}
	}
	if lines[0]["database_id"] != forged || lines[1]["group_id"] != forged || lines[2]["database_id"] != forged {
		t.Fatalf("values must be escaped, not altered: %v", lines)
	}
	if lines[2]["query"] != sql || lines[3]["query"] != sql {
		t.Fatalf("SQL text must keep its line breaks: %q", lines[2]["query"])
	}
	if strings.ContainsRune(buf.String(), '\x1b') || strings.ContainsRune(buf.String(), '\r') {
		t.Fatal("raw control characters reached the log output")
	}
}

func TestWithKeepsContextHandler(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo).With("component", "test")
	ctx := WithRequestInfo(context.Background(), &RequestInfo{CorrelationID: "req-2"})
	l.InfoContext(ctx, "x")
	if decodeLines(t, &buf)[0]["correlation_id"] != "req-2" {
		t.Fatal("logger.With() menghilangkan contextHandler (WithAttrs belum di-override?)")
	}
}

func TestStartSQLFailure(t *testing.T) {
	rec := recordSpans(t)
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	q := SQLLog{DatabaseID: "db1", Kind: "read_only", Query: "SELECT * FROM nope", Params: []any{"budi", 3}}
	_, end := StartSQL(context.Background(), l, q)
	end(-1, &pgconn.PgError{Code: "42P01", Message: "relation does not exist"})

	lines := decodeLines(t, &buf)
	if lines[0]["event"] != "sql.start" || lines[1]["event"] != "sql.result" {
		t.Fatalf("urutan event salah: %v", lines)
	}
	if lines[1]["sqlstate"] != "42P01" || lines[1]["level"] != "ERROR" {
		t.Fatalf("sqlstate/level salah: %v", lines[1])
	}
	if strings.Contains(buf.String(), "budi") {
		t.Fatal("nilai parameter tidak boleh di-log")
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	sp := spans[0]
	if sp.Name() != "sql read_only" || sp.Status().Code != codes.Error {
		t.Fatalf("span name/status = %q/%v", sp.Name(), sp.Status().Code)
	}
	attrs := map[string]string{}
	for _, kv := range sp.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs["db.operation"] != "SELECT" || attrs["db.response.status_code"] != "42P01" || attrs["database_id"] != "db1" {
		t.Fatalf("span attributes = %v", attrs)
	}
	// Both records belong to the SQL span.
	sid := sp.SpanContext().SpanID().String()
	if lines[0]["span_id"] != sid || lines[1]["span_id"] != sid {
		t.Fatalf("log records not in the SQL span %s: %v / %v", sid, lines[0]["span_id"], lines[1]["span_id"])
	}
}

func TestTraceIDsOnRecords(t *testing.T) {
	recordSpans(t)
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)

	l.InfoContext(context.Background(), "no.span")
	ctx, span := otel.Tracer("test").Start(context.Background(), "work")
	l.InfoContext(ctx, "in.span")
	span.End()

	lines := decodeLines(t, &buf)
	if _, ok := lines[0]["trace_id"]; ok {
		t.Fatalf("record without a span must not have trace_id: %v", lines[0])
	}
	if lines[1]["trace_id"] != span.SpanContext().TraceID().String() ||
		lines[1]["span_id"] != span.SpanContext().SpanID().String() {
		t.Fatalf("trace fields wrong: %v", lines[1])
	}
}

func TestOperation(t *testing.T) {
	for in, want := range map[string]string{
		"select 1":                             "SELECT",
		"  -- note\n/* c */ (SELECT 1)":        "SELECT",
		"UPDATE t SET a = 1":                   "UPDATE",
		"with x as (select 1) select * from x": "WITH",
		"":                                     "",
	} {
		if got := operation(in); got != want {
			t.Errorf("operation(%q) = %q, want %q", in, got, want)
		}
	}
}
