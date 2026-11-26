package dbmanager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"dbplatform/internal/logging"
)

func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
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

func TestExecuteQueryOnDB_LogsReadOnlySQL_Integration(t *testing.T) {
	reg, _, _ := introspectionFixture(t)
	var buf bytes.Buffer
	reg.SetLogger(logging.New(&buf, slog.LevelInfo))
	ctx := logging.WithRequestInfo(context.Background(), &logging.RequestInfo{CorrelationID: "req-q"})

	if _, err := reg.ExecuteQueryOnDB(ctx, "target", "SELECT id FROM customers ORDER BY id"); err != nil {
		t.Fatalf("ExecuteQueryOnDB: %v", err)
	}
	recs := decodeRecords(t, &buf)
	if len(recs) != 2 || recs[0]["event"] != "sql.start" || recs[1]["event"] != "sql.result" {
		t.Fatalf("want sql.start + sql.result, got %v", recs)
	}
	ok := recs[1]
	// encoding/json decodes numbers as float64.
	if ok["sql_kind"] != "read_only" || ok["status"] != "SUCCESS" || ok["rows"] != float64(3) ||
		ok["database_id"] != "target" || ok["correlation_id"] != "req-q" {
		t.Errorf("sql.result = %v, want read_only SUCCESS rows=3 with correlation_id", ok)
	}

	buf.Reset()
	if _, err := reg.ExecuteQueryOnDB(ctx, "target", "SELECT * FROM tidak_ada"); err == nil {
		t.Fatal("query on a missing table should fail")
	}
	recs = decodeRecords(t, &buf)
	failed := recs[len(recs)-1]
	if failed["level"] != "ERROR" || failed["status"] != "FAILED" || failed["sqlstate"] != "42P01" {
		t.Errorf("failed sql.result = %v, want ERROR FAILED 42P01", failed)
	}
}

func TestCatalogAndBrowseQueriesAreLogged_Integration(t *testing.T) {
	reg, main, _ := introspectionFixture(t)
	var buf bytes.Buffer
	reg.SetLogger(logging.New(&buf, slog.LevelInfo))
	ctx := context.Background()

	if _, err := reg.ListTables(ctx, "target"); err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if _, err := reg.InspectTableDataAndSchema(ctx, "target", main, "orders", 2, 10); err != nil {
		t.Fatalf("InspectTableDataAndSchema: %v", err)
	}

	var results []map[string]any
	for _, r := range decodeRecords(t, &buf) {
		if r["event"] == "sql.result" {
			results = append(results, r)
		}
	}
	// ListTables, then Inspect: describe, count, page of rows.
	kinds := []string{"catalog", "catalog", "catalog", "table_browse"}
	if len(results) != len(kinds) {
		t.Fatalf("want %d sql.result records, got %d: %v", len(kinds), len(results), results)
	}
	for i, k := range kinds {
		if results[i]["sql_kind"] != k || results[i]["status"] != "SUCCESS" {
			t.Errorf("record %d = %v, want %s SUCCESS", i, results[i], k)
		}
	}
	browse := results[3]
	if browse["rows"] != float64(10) || browse["page"] != float64(2) || browse["table"] != main+".orders" {
		t.Errorf("table_browse = %v, want 10 rows of page 2 of %s.orders", browse, main)
	}
	if q, _ := browse["query"].(string); !strings.Contains(q, "LIMIT 10 OFFSET 10") {
		t.Errorf("table_browse query = %q, want the paged SELECT", q)
	}
}
