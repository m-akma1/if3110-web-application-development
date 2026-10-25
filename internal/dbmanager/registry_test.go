package dbmanager_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"dbplatform/domain"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/ids"
	"dbplatform/internal/testutil"
)

// introspectionFixture creates two schemas on a schema-isolated test database:
// the test's own schema (main) and main_b, each with a "customers" table of a different shape.
func introspectionFixture(t *testing.T) (reg *dbmanager.ConnectionRegistry, main, other string) {
	t.Helper()
	db := testutil.NewSchemaDB(t)
	ctx := context.Background()
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&main); err != nil {
		t.Fatal(err)
	}
	other = main + "_b"
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, other))
	})

	stmts := []string{
		`CREATE TABLE customers (
			id    serial PRIMARY KEY,
			email varchar(160) NOT NULL UNIQUE,
			tier  text NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'pro'))
		)`,
		`CREATE TABLE orders (
			id          bigint PRIMARY KEY,
			customer_id int NOT NULL REFERENCES customers(id),
			status      text,
			total       numeric(14,2),
			created_at  timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX orders_customer_idx ON orders (customer_id)`,
		`CREATE INDEX orders_status_hash ON orders USING hash (status)`,
		`CREATE INDEX orders_created_brin ON orders USING brin (created_at)`,
		`CREATE INDEX orders_lower_status ON orders (lower(status)) INCLUDE (total)`,
		`CREATE TABLE "Order Items" ("Qty" int)`,
		`CREATE TABLE "delete" (x int)`, // a keyword as a table name must still be viewable
		`INSERT INTO customers (email) SELECT 'c' || g || '@x.test' FROM generate_series(1, 3) g`,
		// inserted in reverse so the page order can only come from ORDER BY id
		`INSERT INTO orders (id, customer_id, status) SELECT g, 1 + g % 3, 'S' || g FROM generate_series(30, 1, -1) g`,
		fmt.Sprintf(`CREATE SCHEMA %q`, other),
		fmt.Sprintf(`CREATE TABLE %q.customers (code text PRIMARY KEY)`, other),
		fmt.Sprintf(`INSERT INTO %q.customers VALUES ('only-in-b')`, other),
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("fixture %q: %v", s, err)
		}
	}

	reg = dbmanager.NewConnectionRegistryWithLimits(dbmanager.QueryLimits{Timeout: 5 * time.Second, MaxRows: 100})
	reg.RegisterPool("target", db)
	return reg, main, other
}

func TestListTables_AllUserSchemas_Integration(t *testing.T) {
	reg, main, other := introspectionFixture(t)
	tables, err := reg.ListTables(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	// The test database holds other tests' schemas too, so look only at ours.
	var got []string
	for _, tb := range tables {
		if tb.Schema == main || tb.Schema == other {
			got = append(got, tb.Schema+"."+tb.Name)
		}
		if tb.Schema == "pg_catalog" || tb.Schema == "information_schema" || strings.HasPrefix(tb.Schema, "pg_") {
			t.Errorf("system table listed: %s.%s", tb.Schema, tb.Name)
		}
	}
	// Schemas come out grouped in order; name order within a schema follows the database collation.
	want := []string{main + ".Order Items", main + ".customers", main + ".delete", main + ".orders", other + ".customers"}
	if len(got) != len(want) || !strings.HasPrefix(got[len(got)-1], other+".") {
		t.Fatalf("tables:\n got %v\nwant %v", got, want)
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if strings.Join(sorted, "|") != strings.Join(want, "|") {
		t.Fatalf("tables:\n got %v\nwant %v", got, want)
	}
}

func TestInspectTable_SchemaMetadata_Integration(t *testing.T) {
	reg, main, _ := introspectionFixture(t)
	res, err := reg.InspectTableDataAndSchema(context.Background(), "target", main, "orders", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.DatabaseID != "target" || res.SchemaName != main || res.TableName != "orders" || !res.HasSchema {
		t.Fatalf("identity: %+v", res)
	}

	cols := map[string]domain.ColumnInfo{}
	for _, c := range res.ColumnsMeta {
		cols[c.Name] = c
	}
	if c := cols["id"]; !c.IsPK || c.IsNullable || c.DataType != "bigint" {
		t.Errorf("id column: %+v", c)
	}
	if c := cols["total"]; c.DataType != "numeric(14,2)" || !c.IsNullable {
		t.Errorf("total column: %+v", c)
	}
	if c := cols["created_at"]; c.DefaultValue != "now()" || c.DataType != "timestamp with time zone" {
		t.Errorf("created_at column: %+v", c)
	}

	var fk, pk bool
	for _, c := range res.Constraints {
		switch c.Type {
		case "FOREIGN KEY":
			fk = strings.Contains(c.Definition, "REFERENCES") && strings.Contains(c.Definition, "customers(id)")
		case "PRIMARY KEY":
			pk = c.Definition == "PRIMARY KEY (id)"
		}
	}
	if !fk || !pk {
		t.Errorf("constraints: %+v", res.Constraints)
	}

	idx := map[string]domain.IndexInfo{}
	for _, ix := range res.Indexes {
		idx[ix.Name] = ix
	}
	checks := []struct {
		name, typ, cols string
		unique          bool
	}{
		{"orders_pkey", "BTREE", "id", true},
		{"orders_customer_idx", "BTREE", "customer_id", false},
		{"orders_status_hash", "HASH", "status", false},
		{"orders_created_brin", "BRIN", "created_at", false},
		{"orders_lower_status", "BTREE", "lower(status)", false}, // INCLUDE columns are not key columns
	}
	for _, c := range checks {
		ix, ok := idx[c.name]
		if !ok || ix.IndexType != c.typ || ix.Columns != c.cols || ix.IsUnique != c.unique || !strings.HasPrefix(ix.Definition, "CREATE ") {
			t.Errorf("index %s: got %+v, want type=%s cols=%s unique=%v", c.name, ix, c.typ, c.cols, c.unique)
		}
	}

	// UNIQUE and CHECK on the other table
	cust, err := reg.InspectTableDataAndSchema(context.Background(), "target", main, "customers", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, c := range cust.Constraints {
		types[c.Type] = true
	}
	if !types["UNIQUE"] || !types["CHECK"] || !types["PRIMARY KEY"] {
		t.Errorf("customers constraints: %+v", cust.Constraints)
	}
}

func TestInspectTable_PagingIsOrderedAndCounted_Integration(t *testing.T) {
	reg, main, _ := introspectionFixture(t)
	ctx := context.Background()

	page2, err := reg.InspectTableDataAndSchema(ctx, "target", main, "orders", 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page2.TotalRows != 30 || page2.TotalPages != 3 || page2.CurrentPage != 2 || len(page2.Rows) != 10 {
		t.Fatalf("paging: total=%d pages=%d page=%d rows=%d", page2.TotalRows, page2.TotalPages, page2.CurrentPage, len(page2.Rows))
	}
	if page2.Columns[0] != "id" || page2.Rows[0][0] != "11" || page2.Rows[9][0] != "20" {
		t.Fatalf("page 2 should hold ids 11..20 in order, got first=%s last=%s", page2.Rows[0][0], page2.Rows[9][0])
	}

	last, err := reg.InspectTableDataAndSchema(ctx, "target", main, "orders", 9, 10)
	if err != nil || len(last.Rows) != 0 {
		t.Fatalf("a page past the end is empty, not an error: %v, %d rows", err, len(last.Rows))
	}
}

func TestInspectTable_AddressedBySchema_Integration(t *testing.T) {
	reg, main, other := introspectionFixture(t)
	ctx := context.Background()

	b, err := reg.InspectTableDataAndSchema(ctx, "target", other, "customers", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaName != other || len(b.Rows) != 1 || b.Rows[0][0] != "only-in-b" {
		t.Fatalf("schema %s customers: %+v", other, b)
	}

	// Unusual names are quoted, never formatted into SQL as-is.
	for _, name := range []string{"Order Items", "delete"} {
		res, err := reg.InspectTableDataAndSchema(ctx, "target", main, name, 1, 10)
		if err != nil || res.TableName != name {
			t.Errorf("table %q: %v", name, err)
		}
	}

	// Empty schema resolves through search_path (the main schema here).
	d, err := reg.DescribeTable(ctx, "target", "", "customers")
	if err != nil || d.SchemaName != main || len(d.ColumnsMeta) != 3 {
		t.Fatalf("describe unqualified customers: %v %+v", err, d)
	}
}

func TestInspectTable_Errors_Integration(t *testing.T) {
	reg, main, _ := introspectionFixture(t)
	ctx := context.Background()

	notFound := [][2]string{
		{main, "nope"},
		{"pg_catalog", "pg_authid"},              // system catalogs are not viewer tables
		{"information_schema", "tables"},         // nor is information_schema
		{main, `orders"; DROP TABLE orders; --`}, // injection attempt is just an unknown name
	}
	for _, nt := range notFound {
		if _, err := reg.InspectTableDataAndSchema(ctx, "target", nt[0], nt[1], 1, 10); !errors.Is(err, dbmanager.ErrTableNotFound) {
			t.Errorf("%s.%s: got %v, want ErrTableNotFound", nt[0], nt[1], err)
		}
	}

	if _, err := reg.ListTables(ctx, "missing"); !errors.Is(err, dbmanager.ErrNoPool) {
		t.Errorf("ListTables without pool: %v", err)
	}
	if _, err := reg.InspectTableDataAndSchema(ctx, "missing", main, "orders", 1, 10); !errors.Is(err, dbmanager.ErrNoPool) {
		t.Errorf("Inspect without pool: %v", err)
	}
	if _, err := reg.ExecuteQueryOnDB(ctx, "missing", "SELECT 1"); !errors.Is(err, dbmanager.ErrNoPool) {
		t.Errorf("Query without pool: %v", err)
	}
}

// The target role the platform uses is a regular role (docker/target-init). Even if a server
// function got past the guard, the database itself refuses it.
func TestNonSuperuserTargetRole_CannotReadServerFiles_Integration(t *testing.T) {
	base := testutil.TestDatabaseURL(t)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx := context.Background()

	role := "t_role_" + strings.ReplaceAll(ids.New(), "-", "")[:12]
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE ROLE %q LOGIN NOSUPERUSER PASSWORD 'pw-for-test'`, role)); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.User = url.UserPassword(role, "pw-for-test")
	pool, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, role))
	})

	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var content string
	err = tx.QueryRowContext(ctx, `SELECT pg_read_file('postgresql.conf')`).Scan(&content)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("a regular role must not read server files, got err=%v", err)
	}
}
