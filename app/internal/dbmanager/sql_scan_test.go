package dbmanager_test

import (
	"errors"
	"testing"

	"dbplatform/app/internal/dbmanager"
)

func TestSingleTableSource(t *testing.T) {
	type want struct {
		schema, table string
		ok            bool
	}
	cases := []struct {
		query string
		want  want
	}{
		{"SELECT * FROM customers", want{"", "customers", true}},
		{"select id, email from Customers where id > 3 order by id limit 5;", want{"", "customers", true}},
		{`SELECT * FROM sales."Order Items" oi WHERE oi.qty > 1`, want{"sales", "Order Items", true}},
		{"SELECT c.* FROM public.customers AS c", want{"public", "customers", true}},
		{"SELECT * FROM ONLY customers", want{"", "customers", true}},
		{"SELECT count(*) FROM orders GROUP BY status", want{"", "orders", true}},
		{"SELECT EXTRACT(YEAR FROM created_at) FROM orders", want{"", "orders", true}},
		{"SELECT 'x FROM y JOIN z' AS s FROM orders", want{"", "orders", true}},
		{"SELECT $$ FROM a, b $$ FROM orders -- FROM other\n", want{"", "orders", true}},
		{"SELECT * FROM orders /* , customers */ WHERE id = 1", want{"", "orders", true}},

		{"SELECT 1", want{}},
		{"SELECT * FROM a JOIN b ON a.id = b.a_id", want{}},
		{"SELECT * FROM a NATURAL JOIN b", want{}},
		{"SELECT * FROM a, b", want{}},
		{"SELECT * FROM a x, b y", want{}},
		{"SELECT * FROM (SELECT * FROM a) s", want{}},
		{"SELECT * FROM a WHERE id IN (SELECT a_id FROM b)", want{}},
		{"SELECT (SELECT max(id) FROM b) FROM a", want{}},
		{"WITH x AS (SELECT * FROM a) SELECT * FROM x", want{}},
		{"SELECT * FROM a UNION SELECT * FROM b", want{}},
		{"SELECT * FROM generate_series(1, 10)", want{}},
		{"SELECT * FROM db.sch.tbl", want{}},
		{"SELECT * FROM a TABLESAMPLE SYSTEM (10)", want{}},
		{"SHOW search_path", want{}},
	}
	for _, tc := range cases {
		schema, table, ok := dbmanager.SingleTableSource(tc.query)
		if (want{schema, table, ok}) != tc.want {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q, %v)", tc.query, schema, table, ok, tc.want.schema, tc.want.table, tc.want.ok)
		}
	}
}

func TestValidateReadOnlySQL_BlocksServerFunctions(t *testing.T) {
	blocked := map[string]string{
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity": "pg_terminate_backend",
		"SELECT pg_catalog.pg_cancel_backend(123)":               "pg_cancel_backend",
		`SELECT "pg_read_file"('/etc/passwd')`:                   "pg_read_file",
		"SELECT PG_ADVISORY_LOCK (42)":                           "pg_advisory_lock",
		"SELECT * FROM dblink('host=x', 'select 1') AS t(a int)": "dblink",
		"SELECT lo_export(1, '/tmp/x')":                          "lo_export",
	}
	for q, fn := range blocked {
		_, got, err := dbmanager.ValidateReadOnlySQL(q)
		if !errors.Is(err, dbmanager.ErrBlockedFunction) || got != fn {
			t.Errorf("%q: got (%q, %v), want %s blocked", q, got, err, fn)
		}
	}

	allowed := []string{
		"SELECT 'pg_terminate_backend(1)' AS text_only",
		"SELECT pg_terminate_backend FROM audit_columns", // a column, not a call
		"SELECT pg_try_advisory_xact_lock(1)",            // released with the transaction
		"SELECT pg_sleep(0.1)",                           // bounded by statement_timeout
		"SELECT $q$ pg_read_file('x') $q$",
	}
	for _, q := range allowed {
		if _, kw, err := dbmanager.ValidateReadOnlySQL(q); err != nil {
			t.Errorf("%q should pass, got %v (%s)", q, err, kw)
		}
	}
}
