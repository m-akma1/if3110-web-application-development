package migration

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareBatchRewritesTheStatement(t *testing.T) {
	cases := []struct {
		name, script, table, statement string
	}{
		{
			name:      "update without WHERE",
			script:    "UPDATE orders SET status = 'ARCHIVED'",
			table:     "orders",
			statement: "UPDATE orders SET status = 'ARCHIVED'\nWHERE ($1::bigint IS NULL OR orders.id > $1) AND orders.id <= $2::bigint",
		},
		{
			name:      "update with WHERE keeps the condition in parentheses",
			script:    "UPDATE orders SET status = 'ARCHIVED' WHERE status = 'CANCELLED' OR status = 'PENDING';",
			table:     "orders",
			statement: "UPDATE orders SET status = 'ARCHIVED' WHERE (status = 'CANCELLED' OR status = 'PENDING')\n  AND ($1::bigint IS NULL OR orders.id > $1) AND orders.id <= $2::bigint",
		},
		{
			name:      "alias is used to qualify the cursor",
			script:    "UPDATE orders AS o SET total = o.total * 2 FROM customers c WHERE c.id = o.customer_id",
			table:     "orders",
			statement: "UPDATE orders AS o SET total = o.total * 2 FROM customers c WHERE (c.id = o.customer_id)\n  AND ($1::bigint IS NULL OR o.id > $1) AND o.id <= $2::bigint",
		},
		{
			name:      "schema-qualified, quoted, delete",
			script:    `DELETE FROM public."Events" e WHERE e.kind = 'tmp'`,
			table:     `public."Events"`,
			statement: "DELETE FROM public.\"Events\" e WHERE (e.kind = 'tmp')\n  AND ($1::bigint IS NULL OR e.id > $1) AND e.id <= $2::bigint",
		},
		{
			name:      "WHERE inside a subquery or string is not the statement's WHERE",
			script:    "UPDATE orders SET note = 'x WHERE y', total = (SELECT max(p) FROM prices WHERE prices.o = orders.id)",
			table:     "orders",
			statement: "UPDATE orders SET note = 'x WHERE y', total = (SELECT max(p) FROM prices WHERE prices.o = orders.id)\nWHERE ($1::bigint IS NULL OR orders.id > $1) AND orders.id <= $2::bigint",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := PrepareBatch(c.script, "id")
			if err != nil {
				t.Fatalf("PrepareBatch: %v", err)
			}
			if p.Table != c.table || p.Statement != c.statement {
				t.Errorf("got table %q statement\n%s\nwant table %q statement\n%s", p.Table, p.Statement, c.table, c.statement)
			}
			if !strings.Contains(p.Bounds, "FROM "+c.table+" WHERE") || !strings.Contains(p.Bounds, "LIMIT $2") {
				t.Errorf("bounds query = %s", p.Bounds)
			}
		})
	}
}

func TestPrepareBatchRejects(t *testing.T) {
	cases := map[string]struct {
		script, attr string
		want         error
	}{
		"two statements":       {"UPDATE a SET x = 1; UPDATE b SET x = 1", "id", ErrBatchScript},
		"insert":               {"INSERT INTO a SELECT * FROM b", "id", ErrBatchScript},
		"ddl":                  {"ALTER TABLE a ADD COLUMN x int", "id", ErrBatchScript},
		"with":                 {"WITH s AS (SELECT 1) UPDATE a SET x = 1", "id", ErrBatchScript},
		"returning":            {"UPDATE a SET x = 1 RETURNING id", "id", ErrBatchScript},
		"where current of":     {"UPDATE a SET x = 1 WHERE CURRENT OF c", "id", ErrBatchScript},
		"upper-case attribute": {"UPDATE a SET x = 1", "ID", ErrBatchConfig},
		"injection attribute":  {"UPDATE a SET x = 1", "id; DROP TABLE a", ErrBatchConfig},
		"empty attribute":      {"UPDATE a SET x = 1", "", ErrBatchConfig},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareBatch(c.script, c.attr); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}
