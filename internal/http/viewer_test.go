package http_test

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/groups"
	"dbplatform/views/components"
)

// Strings that only the removed placeholder ("mock") viewer data contained.
var mockDataMarkers = []string{"analytics_db", "usr-101", "alice@email.com", "PT Nusantara Digital", "migration_histories"}

// A registered database without a live connection shows an error state, never placeholder data.
func TestViewer_NoConnection_ShowsErrorState_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	target := accesstest.InsertTarget(t, env.db, "Offline DB")
	accesstest.Grant(t, env.db, groups.PublicGroupID, target, "VIEW_ONLY")
	c := loggedIn(t, env.srv, "carol")

	page := c.get("/dashboard?db_id=" + target)
	body := page.Body.String()
	if page.Code != http.StatusServiceUnavailable || !strings.Contains(body, "Database tidak tersedia") || !strings.Contains(body, "Coba lagi") {
		t.Fatalf("viewer page: status %d, want 503 with error state", page.Code)
	}
	if !strings.Contains(body, `href="`+html.EscapeString(components.ViewerURL(target))+`"`) {
		t.Errorf("retry link should reload the same database")
	}

	frag := c.get(components.TableURL(target, "public", "customers", 2))
	if frag.Code != http.StatusServiceUnavailable || !strings.Contains(frag.Body.String(), "Database tidak tersedia") {
		t.Errorf("table fragment: status %d, want 503 with error state", frag.Code)
	}
	query := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"SELECT * FROM customers"}})
	if query.Code != http.StatusServiceUnavailable || !strings.Contains(query.Body.String(), "Database tidak tersedia") {
		t.Errorf("query: status %d, want 503 with error state", query.Code)
	}

	for _, rec := range []string{body, frag.Body.String(), query.Body.String()} {
		for _, m := range mockDataMarkers {
			if strings.Contains(rec, m) {
				t.Fatalf("placeholder data %q shown instead of an error", m)
			}
		}
	}
}

func TestViewer_LiveTables_PagingAndSchemaPanel_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "Live DB")
	accesstest.Grant(t, env.db, groups.PublicGroupID, target, "VIEW_ONLY")
	pool := env.connectTarget(t, target)

	var schema string
	if err := pool.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE orders (id int PRIMARY KEY, note text)`,
		`CREATE INDEX orders_note_hash ON orders USING hash (note)`,
		`INSERT INTO orders SELECT g, 'n' || g FROM generate_series(1, 60) g`,
		`CREATE TABLE notes (order_id int REFERENCES orders(id))`,
	} {
		if _, err := pool.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	c := loggedIn(t, env.srv, "carol")

	// Page: tables are listed under their schema and link to schema-qualified fragments.
	page := c.get("/dashboard?db_id=" + target).Body.String()
	if !strings.Contains(page, `data-key="`+schema+`.orders"`) ||
		!strings.Contains(page, html.EscapeString(components.TableURL(target, schema, "orders", 1))) {
		t.Fatalf("viewer page should list %s.orders with a schema-qualified link", schema)
	}

	// Fragment: page 2 of 3, and both pager links keep db_id and schema (B1).
	frag := c.get(components.TableURL(target, schema, "orders", 2))
	body := frag.Body.String()
	if frag.Code != http.StatusOK || !strings.Contains(body, "Halaman 2 dari 3") || !strings.Contains(body, ">26</td>") {
		t.Fatalf("page 2 (rows 26-50 of 60): status %d", frag.Code)
	}
	for _, p := range []int{1, 3} {
		link := html.EscapeString(components.TableURL(target, schema, "orders", p))
		if !strings.Contains(body, `hx-get="`+link+`"`) {
			t.Errorf("pager link to page %d should be %s", p, link)
		}
	}
	if !strings.Contains(body, "HASH") || !strings.Contains(body, "<span>Schema Viewer</span>") {
		t.Errorf("table view should show its schema with the real index type")
	}

	if rec := c.get(components.TableURL(target, schema, "missing_table", 1)); rec.Code != http.StatusNotFound {
		t.Errorf("unknown table: status %d, want 404", rec.Code)
	}

	// Query Explorer: the schema panel only for a plain single-table SELECT (B3).
	single := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"SELECT id FROM orders WHERE id < 5"}}).Body.String()
	if !strings.Contains(single, "<span>Schema Viewer</span>") || !strings.Contains(single, "Hasil Query") || strings.Contains(single, "Selanjutnya") {
		t.Errorf("single-table query should show the schema panel and no pager")
	}
	for _, q := range []string{
		"SELECT o.id FROM orders o JOIN notes n ON n.order_id = o.id",
		"SELECT * FROM notes WHERE order_id IN (SELECT id FROM orders)",
		"SELECT 1 AS x",
	} {
		rec := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {q}})
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "<span>Schema Viewer</span>") {
			t.Errorf("%q: status %d, results only expected", q, rec.Code)
		}
	}

	// Errors in the query are shown to the user (422), blocked server functions too.
	if rec := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"SELECT nope FROM orders"}}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("bad column: status %d", rec.Code)
	}
	rec := c.post("/dashboard/query", url.Values{"db_id": {target}, "sql_query": {"SELECT pg_terminate_backend(pid) FROM pg_stat_activity"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "pg_terminate_backend") {
		t.Errorf("blocked function: status %d %s", rec.Code, rec.Body.String())
	}
}

// The navbar switcher reloads the page the user is on, with ?db_id= in the URL (B2).
func TestNavbarSwitcher_StaysOnCurrentPage_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	target := accesstest.InsertTarget(t, env.db, "Switch DB")
	accesstest.Grant(t, env.db, groups.PublicGroupID, target, "VIEW_ONLY")
	c := loggedIn(t, env.srv, "carol")

	cases := map[string]string{
		"/dashboard?db_id=" + target: `action="/dashboard"`,
		"/migration?db_id=" + target: `action="/migration"`,
	}
	for path, want := range cases {
		body := c.get(path).Body.String()
		if !strings.Contains(body, `id="db-switcher"`) || !strings.Contains(body, want) || strings.Contains(body, "/dashboard/select-db") {
			t.Errorf("%s: switcher should submit to the same page (%s)", path, want)
		}
	}
}
