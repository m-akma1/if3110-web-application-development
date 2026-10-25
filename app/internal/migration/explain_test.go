package migration

import (
	"context"
	"testing"
)

func TestExplainable(t *testing.T) {
	cases := map[string]bool{
		"SELECT * FROM t":                                    true,
		"WITH x AS (SELECT 1) SELECT * FROM x":               true,
		"INSERT INTO t VALUES (1)":                           true,
		"UPDATE t SET x = 1":                                 true,
		"DELETE FROM t":                                      true,
		"MERGE INTO t USING s ON true DO NOTHING":            true,
		"VALUES (1), (2)":                                    true,
		"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x": true,
		"SELECT * INTO copy_of_t FROM t":                     true, // runs as CREATE TABLE AS
		"CREATE TABLE t (x int)":                             false,
		"ALTER TABLE t ADD COLUMN y int":                     false,
		"ANALYZE t":                                          false,
		"EXPLAIN SELECT 1":                                   false,
	}
	for sql, want := range cases {
		st := SplitStatements(sql)
		if got := explainable(st[0].Words); got != want {
			t.Errorf("explainable(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestParsePlanAndFlatten(t *testing.T) {
	raw := `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Inner", "Startup Cost": 1.5, "Total Cost": 40.25,
		"Plan Rows": 10, "Actual Rows": 5000, "Actual Loops": 1, "Actual Total Time": 3.2, "Hash Cond": "(o.customer_id = c.id)",
		"Plans": [
			{"Node Type": "Seq Scan", "Parent Relationship": "Outer", "Relation Name": "orders", "Alias": "o",
			 "Plan Rows": 5000, "Actual Rows": 5000, "Actual Loops": 1, "Shared Hit Blocks": 12},
			{"Node Type": "Hash", "Parent Relationship": "Inner", "Plan Rows": 100, "Actual Rows": 100, "Actual Loops": 1,
			 "Plans": [{"Node Type": "Index Scan", "Relation Name": "customers", "Index Name": "customers_pkey",
			            "Index Cond": "(id > 0)", "Plan Rows": 100, "Actual Rows": 0, "Actual Loops": 0}]}
		]},
		"Planning Time": 0.2, "Execution Time": 3.5}]`
	p, err := parsePlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.PlanningTime != 0.2 || p.ExecutionTime != 3.5 {
		t.Fatalf("timings = %v / %v", p.PlanningTime, p.ExecutionTime)
	}
	rows := p.Rows()
	wantTypes := []string{"Hash Join", "Seq Scan", "Hash", "Index Scan"}
	wantDepth := []int{0, 1, 1, 2}
	if len(rows) != len(wantTypes) {
		t.Fatalf("got %d rows, want %d", len(rows), len(wantTypes))
	}
	for i, r := range rows {
		if r.Node.NodeType != wantTypes[i] || r.Depth != wantDepth[i] {
			t.Errorf("row %d = %s at depth %d, want %s at %d", i, r.Node.NodeType, r.Depth, wantTypes[i], wantDepth[i])
		}
	}
	root, scan, idx := rows[0].Node, rows[1].Node, rows[3].Node
	if root.JoinType != "Inner" || root.HashCond == "" || scan.RelationName != "orders" || scan.SharedHitBlocks != 12 || idx.IndexName != "customers_pkey" {
		t.Fatalf("fields not decoded: %+v / %+v / %+v", root, scan, idx)
	}
	if !root.Misestimated() {
		t.Error("10 estimated vs 5000 actual rows should be flagged")
	}
	if scan.Misestimated() {
		t.Error("an exact estimate should not be flagged")
	}
	if !idx.NeverExecuted() || idx.Misestimated() {
		t.Error("a node with 0 loops is never executed and cannot be misestimated")
	}

	for _, bad := range []string{"", "[]", "{"} {
		if _, err := parsePlan(bad); err == nil {
			t.Errorf("parsePlan(%q) should fail", bad)
		}
	}
}

func TestDryRunExplainSeesEarlierStatements(t *testing.T) {
	f := newFixture(t, Limits{})
	script := `CREATE TABLE e (id int, v int);
INSERT INTO e SELECT g, g FROM generate_series(1, 5000) g;
ANALYZE e;
SELECT * FROM e WHERE id = 42;
CREATE INDEX e_id_idx ON e (id);
SELECT * FROM e WHERE id = 42;`

	res, err := f.engine.DryRun(context.Background(), f.dbID, script)
	if err != nil || !res.OK {
		t.Fatalf("DryRun = %+v, %v", res, err)
	}
	if len(res.Steps) != 6 {
		t.Fatalf("got %d steps, want 6", len(res.Steps))
	}
	wantKinds := []StepKind{StepExecuted, StepExplained, StepExecuted, StepExplained, StepExecuted, StepExplained}
	for i, st := range res.Steps {
		if st.Kind != wantKinds[i] || st.Index != i+1 {
			t.Fatalf("step %d = %s, want %s", i+1, st.Kind, wantKinds[i])
		}
	}

	insert := res.Steps[1].Plan
	if insert == nil || insert.Root.NodeType != "ModifyTable" || insert.Root.Operation != "Insert" {
		t.Fatalf("INSERT plan root = %+v", insert)
	}

	before, after := scanOn(res.Steps[3].Plan, "e"), scanOn(res.Steps[5].Plan, "e")
	if before == nil || before.NodeType != "Seq Scan" {
		t.Fatalf("before the index the scan should be a Seq Scan, got %+v", before)
	}
	if before.ActualRows != 1 || before.RowsRemovedByFilter != 4999 {
		t.Fatalf("seq scan actual rows = %v, removed = %v; want 1 and 4999", before.ActualRows, before.RowsRemovedByFilter)
	}
	if after == nil || after.IndexName != "e_id_idx" {
		t.Fatalf("after CREATE INDEX the plan should use e_id_idx, got %+v", after)
	}

	if f.targetHasTable(t, "e") {
		t.Fatal("dry run must leave the target unchanged")
	}
}

func TestDryRunFailureStopsAndPointsAtTheStatement(t *testing.T) {
	f := newFixture(t, Limits{})
	script := "CREATE TABLE f (x int);\nSELECT nope FROM f;\nINSERT INTO f VALUES (1);"
	res, err := f.engine.DryRun(context.Background(), f.dbID, script)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Err == nil || res.Err.Code != "42703" {
		t.Fatalf("result = %+v, want 42703", res.Err)
	}
	// The error came through the EXPLAIN prefix; the position must still point into the script.
	if res.Err.Statement != 2 || res.Err.Line != 2 || res.Err.Column != 8 {
		t.Fatalf("error at statement %d line %d column %d, want 2/2/8", res.Err.Statement, res.Err.Line, res.Err.Column)
	}
	kinds := [3]StepKind{res.Steps[0].Kind, res.Steps[1].Kind, res.Steps[2].Kind}
	if kinds != [3]StepKind{StepExecuted, StepFailed, StepSkipped} {
		t.Fatalf("step kinds = %v, want EXECUTED, FAILED, SKIPPED", kinds)
	}
}

// scanOn returns the first plan node that reads relation.
func scanOn(p *Plan, relation string) *PlanNode {
	if p == nil {
		return nil
	}
	for _, n := range p.Nodes() {
		if n.RelationName == relation {
			return n
		}
	}
	return nil
}
