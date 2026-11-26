package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"dbplatform/internal/logging"
)

// engineLogEvents runs fn with a JSON logger on the engine and returns the decoded records.
func engineLogEvents(t *testing.T, f *fixture, fn func(ctx context.Context)) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	f.engine.SetLogger(logging.New(&buf, slog.LevelInfo))
	ctx := logging.WithRequestInfo(context.Background(), &logging.RequestInfo{CorrelationID: "req-engine", Actor: f.userID})
	fn(ctx)

	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", ln, err)
		}
		out = append(out, m)
	}
	return out
}

func eventNames(recs []map[string]any) []string {
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		names = append(names, r["event"].(string))
	}
	return names
}

func wantEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestExecuteLogsStartResultCommit(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "logged", "CREATE TABLE logged (x int)")

	recs := engineLogEvents(t, f, func(ctx context.Context) {
		if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	})
	wantEvents(t, eventNames(recs), "sql.start", "sql.result", "tx.commit", "migration.attempt")

	for _, r := range recs {
		if r["correlation_id"] != "req-engine" || r["actor"] != f.userID {
			t.Errorf("%v: correlation_id/actor from ctx missing: %v", r["event"], r)
		}
		if r["database_id"] != f.dbID || r["migration_id"] != d.ID || r["execution_type"] != "UP" {
			t.Errorf("%v: migration attributes wrong: %v", r["event"], r)
		}
	}
	if recs[1]["status"] != "SUCCESS" || recs[1]["sql_kind"] != "migration" {
		t.Errorf("sql.result = %v, want SUCCESS migration", recs[1])
	}
	// encoding/json decodes numbers as float64; version is assigned when history is recorded.
	if a := recs[3]; a["status"] != "SUCCESS" || a["version"] != float64(1) || a["execution_id"] != d.ID {
		t.Errorf("migration.attempt = %v, want SUCCESS version 1", a)
	}
}

func TestExecuteFailureLogsSQLStateAndRollback(t *testing.T) {
	f := newFixture(t, Limits{})
	d := f.draft(t, "broken", "INSERT INTO missing VALUES (1)")

	recs := engineLogEvents(t, f, func(ctx context.Context) {
		if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
			t.Fatalf("a failing script is a result, not an error: %v", err)
		}
	})
	wantEvents(t, eventNames(recs), "sql.start", "sql.result", "tx.rollback", "migration.attempt")

	result, rollback := recs[1], recs[2]
	if result["level"] != "ERROR" || result["status"] != "FAILED" || result["sqlstate"] != "42P01" {
		t.Errorf("sql.result = %v, want ERROR FAILED 42P01", result)
	}
	// tx.rollback must carry the script's own error, not a misleading commit error.
	if msg, _ := rollback["error"].(string); !strings.Contains(msg, "missing") {
		t.Errorf("tx.rollback error = %q, want the original SQL error", msg)
	}
	if a := recs[3]; a["level"] != "ERROR" || a["status"] != "FAILED" || a["sqlstate"] != "42P01" || a["version"] != float64(0) {
		t.Errorf("migration.attempt = %v, want ERROR FAILED 42P01 without version", a)
	}
}
