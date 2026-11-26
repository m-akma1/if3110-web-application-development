package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

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

func TestWithKeepsContextHandler(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo).With("component", "test")
	ctx := WithRequestInfo(context.Background(), &RequestInfo{CorrelationID: "req-2"})
	l.InfoContext(ctx, "x")
	if decodeLines(t, &buf)[0]["correlation_id"] != "req-2" {
		t.Fatal("logger.With() menghilangkan contextHandler (WithAttrs belum di-override?)")
	}
}

func TestSQLResultFailure(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo)
	q := SQLLog{DatabaseID: "db1", Kind: "read_only", Query: "SELECT * FROM nope", Params: []any{"budi", 3}}
	start := SQLStart(context.Background(), l, q)
	SQLResult(context.Background(), l, q, start, -1, &pgconn.PgError{Code: "42P01", Message: "relation does not exist"})

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
}
