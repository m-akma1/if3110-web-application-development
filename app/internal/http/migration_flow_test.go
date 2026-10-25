package http_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dbplatform/app/internal/accesstest"
	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/ids"
)

// draftFromRedirect extracts the draft id from an HX-Redirect to /migration?db_id=..&draft=..
func draftFromRedirect(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil || u.Query().Get("draft") == "" {
		t.Fatalf("expected a redirect to a draft, got %q", location)
	}
	return u.Query().Get("draft")
}

func TestMigrationFlow_FailRollbackDuplicateFixSucceed_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()

	target := accesstest.InsertTarget(t, env.db, "PostgreSQL Demo")
	pool := env.connectTarget(t, target)
	editors, _ := env.groups.Create(ctx, "Engineers", "")
	viewers, _ := env.groups.Create(ctx, "Analysts", "")
	accesstest.Grant(t, env.db, editors.ID, target, "VIEW_EDIT")
	accesstest.Grant(t, env.db, viewers.ID, target, "VIEW_ONLY")
	for name, group := range map[string]string{"bob": editors.ID, "alice": viewers.ID} {
		u, err := env.sessions.RegisterUser(ctx, authn.RegisterInput{Username: name, Password: "Password-" + name, Role: authn.RoleUser})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.groups.AddMember(ctx, group, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	bob := newClient(t, env.srv)
	bob.login("bob", "Password-bob")
	alice := newClient(t, env.srv)
	alice.login("alice", "Password-alice")

	tableExists := func(name string) bool {
		var ok bool
		if err := pool.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}

	// Validation errors are shown in the form (422), nothing is saved.
	rec := bob.post("/migration/drafts", url.Values{"db_id": {target}, "name": {"  "}, "sql_script": {"SELECT 1"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "nama migration wajib diisi") {
		t.Fatalf("empty name: %d %s", rec.Code, rec.Body.String())
	}

	// 1. Save a draft whose second statement fails.
	broken := "CREATE TABLE t (x int);\nINSERT INTO missing VALUES (1);"
	rec = bob.post("/migration/drafts", url.Values{"db_id": {target}, "name": {"create t"}, "sql_script": {broken}})
	draft := draftFromRedirect(t, rec.Header().Get("HX-Redirect"))
	page := bob.get("/migration?db_id=" + target + "&draft=" + draft).Body.String()
	if !strings.Contains(page, "create t") || !strings.Contains(page, "INSERT INTO missing") || !strings.Contains(page, "Eksekusi Migration") {
		t.Fatal("the editor should open the saved draft")
	}

	// 2. Dry run reports the failure and changes nothing.
	rec = bob.post("/migration/"+draft+"/dry-run", url.Values{"name": {"create t"}, "sql_script": {broken}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Dry run gagal") || !strings.Contains(rec.Body.String(), "42P01") {
		t.Fatalf("dry run: %d %s", rec.Code, rec.Body.String())
	}
	if tableExists("t") {
		t.Fatal("dry run must not create t")
	}

	// 3. Execute: FAILED, rolled back, shown on the detail page.
	rec = bob.post("/migration/"+draft+"/execute", url.Values{"name": {"create t"}, "sql_script": {broken}})
	if rec.Header().Get("HX-Redirect") != "/migration/"+draft {
		t.Fatalf("execute should open the detail page, got %d %q %s", rec.Code, rec.Header().Get("HX-Redirect"), rec.Body.String())
	}
	if tableExists("t") {
		t.Fatal("the failed run must be rolled back: t must not exist")
	}
	detail := bob.get("/migration/" + draft).Body.String()
	for _, want := range []string{"FAILED", "ROLLBACK", "42P01", "statement 2", "Duplikat sebagai Draft Baru"} {
		if !strings.Contains(detail, want) {
			t.Errorf("FAILED detail page should contain %q", want)
		}
	}
	// Executed migrations are immutable.
	if code := bob.post("/migration/"+draft, url.Values{"name": {"x"}, "sql_script": {"SELECT 1"}}).Code; code != http.StatusUnprocessableEntity {
		t.Fatalf("editing an executed migration: status %d, want 422", code)
	}

	// 4. Duplicate, fix, execute: SUCCESS v1.
	rec = bob.post("/migration/"+draft+"/duplicate", nil)
	fixed := draftFromRedirect(t, rec.Header().Get("HX-Redirect"))
	if fixed == draft {
		t.Fatal("duplicate must create a new draft")
	}
	rec = bob.post("/migration/"+fixed+"/execute", url.Values{"name": {"create t"}, "sql_script": {"CREATE TABLE t (x int);"}})
	if rec.Header().Get("HX-Redirect") != "/migration/"+fixed {
		t.Fatalf("execute fixed: %d %s", rec.Code, rec.Body.String())
	}
	if !tableExists("t") {
		t.Fatal("t should exist after the successful run")
	}
	detail = bob.get("/migration/" + fixed).Body.String()
	for _, want := range []string{"SUCCESS", "v1", "CREATE TABLE t (x int);", "bob"} {
		if !strings.Contains(detail, want) {
			t.Errorf("SUCCESS detail page should contain %q", want)
		}
	}

	// 5. Dry run with EXPLAIN on a new draft: the plan is shown, nothing is kept.
	rec = bob.post("/migration/drafts", url.Values{"db_id": {target}, "name": {"query t"}, "sql_script": {"INSERT INTO t SELECT generate_series(1, 100);\nSELECT * FROM t WHERE x = 7;"}})
	explainDraft := draftFromRedirect(t, rec.Header().Get("HX-Redirect"))
	rec = bob.post("/migration/"+explainDraft+"/dry-run", nil)
	body := rec.Body.String()
	for _, want := range []string{"Dry run berhasil", "EXPLAIN ANALYZE", "Seq Scan on t", "Filter: (x = 7)", "Rows Removed by Filter: 99"} {
		if !strings.Contains(body, want) {
			t.Errorf("dry run report should contain %q:\n%s", want, body)
		}
	}
	var rows int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM t`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("dry run must roll back the INSERT, t has %d rows (%v)", rows, err)
	}

	// 6. Non-transactional scripts are rejected before running; the draft stays editable.
	rec = bob.post("/migration/"+explainDraft+"/execute", url.Values{"name": {"query t"}, "sql_script": {"VACUUM t;"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ditolak") {
		t.Fatalf("VACUUM: %d %s", rec.Code, rec.Body.String())
	}

	// 7. View Only: history and detail of executed runs, but no drafts or editor.
	page = alice.get("/migration?db_id=" + target).Body.String()
	if !strings.Contains(page, "v1") || !strings.Contains(page, "/migration/"+fixed) {
		t.Error("viewer should see the history")
	}
	if strings.Contains(page, "query t") || strings.Contains(page, "Simpan Draft") {
		t.Error("viewer must not see drafts or the editor")
	}
	if code := alice.get("/migration/" + fixed).Code; code != http.StatusOK {
		t.Errorf("viewer detail of an executed run: %d, want 200", code)
	}
	if code := alice.get("/migration/" + explainDraft).Code; code != http.StatusForbidden {
		t.Errorf("viewer detail of a draft: %d, want 403", code)
	}
	if code := alice.post("/migration/"+explainDraft+"/execute", nil).Code; code != http.StatusForbidden {
		t.Errorf("viewer execute: %d, want 403", code)
	}

	// Unknown ids look like no access.
	if code := bob.get("/migration/" + ids.New()).Code; code != http.StatusForbidden {
		t.Errorf("unknown migration: %d, want 403", code)
	}
}

func TestMigrationTargetOffline_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "offline") // registered, but no pool
	g, _ := env.groups.Create(ctx, "Engineers", "")
	accesstest.Grant(t, env.db, g.ID, target, "VIEW_EDIT")
	u, _ := env.sessions.GetUserByUsername(ctx, "caca")
	if err := env.groups.AddMember(ctx, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	c := loggedIn(t, env.srv, "caca")

	rec := c.post("/migration/drafts", url.Values{"db_id": {target}, "name": {"x"}, "sql_script": {"SELECT 1"}})
	draft := draftFromRedirect(t, rec.Header().Get("HX-Redirect"))
	for _, action := range []string{"dry-run", "execute"} {
		rec := c.post("/migration/"+draft+"/"+action, nil)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "tidak terhubung") {
			t.Errorf("%s on an offline target: %d %s", action, rec.Code, rec.Body.String())
		}
	}
}
