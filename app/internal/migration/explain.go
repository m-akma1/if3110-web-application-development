package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bonus: query plan analysis in dry runs.
//
// A dry run sends the script one statement at a time inside a transaction that is always rolled
// back. DML statements (SELECT, INSERT, UPDATE, DELETE, MERGE, ...) go through
// EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON), which runs them for real and returns the plan with
// actual row counts and timings. Every other statement runs as is. Because each statement runs
// after the previous ones, a plan sees the schema the script built so far, for example an
// index created two statements earlier.

const explainPrefix = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "

// explainable reports whether EXPLAIN ANALYZE accepts the statement.
func explainable(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "SELECT", "WITH", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "TABLE":
		return true
	}
	return false
}

// Plan is one EXPLAIN (ANALYZE, FORMAT JSON) result.
type Plan struct {
	Root          PlanNode `json:"Plan"`
	PlanningTime  float64  `json:"Planning Time"`  // ms
	ExecutionTime float64  `json:"Execution Time"` // ms
}

// PlanNode is one node of the plan tree. Only the fields the UI shows are decoded.
type PlanNode struct {
	NodeType           string `json:"Node Type"` // e.g. "Seq Scan", "Index Scan", "Hash Join"
	ParentRelationship string `json:"Parent Relationship"`
	Operation          string `json:"Operation"` // ModifyTable: Insert, Update, Delete, Merge
	RelationName       string `json:"Relation Name"`
	Schema             string `json:"Schema"`
	Alias              string `json:"Alias"`
	IndexName          string `json:"Index Name"`
	JoinType           string `json:"Join Type"`
	Strategy           string `json:"Strategy"` // Aggregate: Plain, Sorted, Hashed

	StartupCost float64 `json:"Startup Cost"`
	TotalCost   float64 `json:"Total Cost"`
	PlanRows    float64 `json:"Plan Rows"`

	ActualStartupTime float64 `json:"Actual Startup Time"` // ms, per loop
	ActualTotalTime   float64 `json:"Actual Total Time"`   // ms, per loop
	ActualRows        float64 `json:"Actual Rows"`         // per loop
	ActualLoops       float64 `json:"Actual Loops"`

	IndexCond           string  `json:"Index Cond"`
	RecheckCond         string  `json:"Recheck Cond"`
	Filter              string  `json:"Filter"`
	JoinFilter          string  `json:"Join Filter"`
	HashCond            string  `json:"Hash Cond"`
	MergeCond           string  `json:"Merge Cond"`
	RowsRemovedByFilter float64 `json:"Rows Removed by Filter"`

	SharedHitBlocks  int64 `json:"Shared Hit Blocks"`
	SharedReadBlocks int64 `json:"Shared Read Blocks"`

	Plans []PlanNode `json:"Plans"`
}

// PlanRow is a node with its depth in the tree, for rendering the tree as an indented list.
type PlanRow struct {
	Depth int
	Node  *PlanNode
}

// Rows flattens the plan tree depth-first.
func (p *Plan) Rows() []PlanRow {
	if p == nil {
		return nil
	}
	var out []PlanRow
	var walk func(n *PlanNode, depth int)
	walk = func(n *PlanNode, depth int) {
		out = append(out, PlanRow{Depth: depth, Node: n})
		for i := range n.Plans {
			walk(&n.Plans[i], depth+1)
		}
	}
	walk(&p.Root, 0)
	return out
}

// Nodes returns every node of the plan, depth-first.
func (p *Plan) Nodes() []*PlanNode {
	rows := p.Rows()
	out := make([]*PlanNode, len(rows))
	for i, r := range rows {
		out[i] = r.Node
	}
	return out
}

// NeverExecuted reports a node the executor skipped (EXPLAIN shows "never executed").
func (n *PlanNode) NeverExecuted() bool { return n.ActualLoops == 0 }

// Misestimated reports a node whose row estimate is off by 10x or more (and by at least 100 rows),
// which usually means stale statistics or a predicate the planner cannot estimate.
func (n *PlanNode) Misestimated() bool {
	if n.NeverExecuted() {
		return false
	}
	est, act := n.PlanRows, n.ActualRows
	hi, lo := max(est, act), min(est, act)
	return hi-lo >= 100 && hi >= 10*max(lo, 1)
}

func parsePlan(raw string) (*Plan, error) {
	var plans []Plan
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, errors.New("EXPLAIN returned no plan")
	}
	return &plans[0], nil
}

// dryRun runs stmts one by one on tx and reports each. It stops at the first failing statement;
// later statements are reported as skipped. The caller rolls tx back.
func dryRun(ctx context.Context, tx *sql.Tx, script string, stmts []Statement) *RunResult {
	res := &RunResult{Statements: len(stmts), Steps: make([]Step, 0, len(stmts))}
	start := time.Now()
	for i, st := range stmts {
		step := Step{Index: i + 1, SQL: st.Text}
		if res.Err != nil {
			step.Kind = StepSkipped
			res.Steps = append(res.Steps, step)
			continue
		}
		t0 := time.Now()
		prefix := ""
		var err error
		if explainable(st.Words) {
			prefix = explainPrefix
			var raw string
			// Simple protocol: no prepared statement is cached for every statement of the script.
			if err = tx.QueryRowContext(ctx, prefix+st.Text, pgx.QueryExecModeSimpleProtocol).Scan(&raw); err == nil {
				step.Kind = StepExplained
				if step.Plan, err = parsePlan(raw); err != nil {
					// The statement ran; only the plan is unreadable.
					step.Kind, step.Plan, err = StepExecuted, nil, nil
				}
			}
		} else {
			_, err = tx.ExecContext(ctx, st.Text)
			step.Kind = StepExecuted
		}
		step.Duration = time.Since(t0)
		if err != nil {
			step.Kind = StepFailed
			res.Err = statementError(script, st, i+1, prefix, err)
		}
		res.Steps = append(res.Steps, step)
	}
	res.Duration = time.Since(start)
	res.OK = res.Err == nil
	return res
}

// statementError is toScriptError for a statement sent on its own (optionally after prefix):
// PostgreSQL's position is relative to what was sent, so shift it back into the whole script.
func statementError(script string, st Statement, index int, prefix string, err error) *ScriptError {
	se := baseScriptError(err)
	se.Statement = index
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Position > 0 {
		if rel := int(pgErr.Position) - utf8.RuneCountInString(prefix); rel >= 1 {
			abs := utf8.RuneCountInString(script[:st.Start]) + rel
			if line, col, _, ok := lineColumn(script, abs); ok {
				se.Line, se.Column = line, col
			}
		}
	}
	return se
}

// baseScriptError copies the PostgreSQL error fields, without position information.
func baseScriptError(err error) *ScriptError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return &ScriptError{Message: pgErr.Message, Code: pgErr.Code, Detail: pgErr.Detail, Hint: pgErr.Hint, Where: pgErr.Where}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ScriptError{Message: "eksekusi melebihi batas waktu dan dibatalkan"}
	}
	return &ScriptError{Message: fmt.Sprint(err)}
}
