package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/migration"
)

// auditEvents returns the audit.* records written so far.
func auditEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var l map[string]any
		if err := json.Unmarshal(line, &l); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		if ev, _ := l["event"].(string); strings.HasPrefix(ev, "audit.") {
			out = append(out, l)
		}
	}
	return out
}

func TestAuditEventsForRequests_Integration(t *testing.T) {
	var buf bytes.Buffer
	env := newTestEnv(t, &buf)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "audited")
	env.connectTarget(t, target)
	editors, err := env.groups.Create(ctx, "Editors", "")
	if err != nil {
		t.Fatal(err)
	}
	bob := userID(t, env.srv, "bob")
	if err := env.groups.AddMember(ctx, editors.ID, bob); err != nil {
		t.Fatal(err)
	}

	// A platform admin grants the group edit access: audit.grant with the admin as actor.
	admin := newClient(t, env.srv)
	admin.login("admin", testPasswords["admin"])
	if rec := admin.postJSON("/api/admin/grants",
		`{"group_id":"`+editors.ID+`","database_id":"`+target+`","level":"VIEW_EDIT"}`); rec.Code != http.StatusOK {
		t.Fatalf("set grant = %d %s", rec.Code, rec.Body)
	}

	d, err := env.migrations.CreateDraft(ctx, migration.NewDraft{DatabaseID: target, Name: "orders", SQL: "CREATE TABLE orders (id int)", SQLDown: "DROP TABLE orders", CreatedBy: bob})
	if err != nil {
		t.Fatal(err)
	}
	editor := newClient(t, env.srv)
	editor.login("bob", testPasswords["bob"])
	if rec := editor.post("/api/migration/"+d.ID+"/execute", url.Values{}); rec.Code != http.StatusAccepted {
		t.Fatalf("execute = %d %s", rec.Code, rec.Body)
	}
	// carol has no access: the same request is audited as denied.
	stranger := newClient(t, env.srv)
	stranger.login("carol", testPasswords["carol"])
	stranger.post("/api/migration/"+d.ID+"/execute", url.Values{})

	events := auditEvents(t, &buf)
	if len(events) != 3 {
		t.Fatalf("audit events = %d %v, want grant, enqueue, denied enqueue", len(events), events)
	}
	grant, enqueue, denied := events[0], events[1], events[2]
	if grant["event"] != "audit.grant" || grant["outcome"] != "success" || grant["actor"] != userID(t, env.srv, "admin") ||
		grant["access_level"] != "VIEW_EDIT" || grant["level"] != "INFO" || grant["database_id"] != target {
		t.Errorf("grant event = %v", grant)
	}
	if enqueue["event"] != "audit.enqueue" || enqueue["outcome"] != "success" || enqueue["actor"] != bob ||
		enqueue["request"] != "execute" || enqueue["migration_id"] != d.ID || enqueue["job_id"] == nil ||
		enqueue["correlation_id"] == nil {
		t.Errorf("enqueue event = %v", enqueue)
	}
	if denied["event"] != "audit.enqueue" || denied["outcome"] != "denied" || denied["actor"] != userID(t, env.srv, "carol") {
		t.Errorf("denied event = %v", denied)
	}
	// Audit events never carry the SQL script, and no attribute may reuse a key slog writes
	// itself: a duplicate JSON key is read back as only one of the two values.
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if bytes.Count(line, []byte(`"level":`)) > 1 || bytes.Count(line, []byte(`"time":`)) > 1 {
			t.Errorf("duplicate key in log line: %s", line)
		}
	}
	for _, e := range events {
		if b, _ := json.Marshal(e); strings.Contains(string(b), "CREATE TABLE") {
			t.Errorf("audit event leaks SQL: %s", b)
		}
	}
}

func TestMigrationExecutionsEndpoint_Integration(t *testing.T) {
	env := newTestEnv(t, nil)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "history")
	env.connectTarget(t, target)
	viewers, err := env.groups.Create(ctx, "Viewers", "")
	if err != nil {
		t.Fatal(err)
	}
	accesstest.Grant(t, env.db, viewers.ID, target, "VIEW_ONLY")
	if err := env.groups.AddMember(ctx, viewers.ID, userID(t, env.srv, "bob")); err != nil {
		t.Fatal(err)
	}
	d, err := env.migrations.CreateDraft(ctx, migration.NewDraft{DatabaseID: target, Name: "orders", SQL: "CREATE TABLE orders (id int)"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := migration.NewEngine(env.db, env.registry, migration.Limits{}).Execute(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}

	viewer := newClient(t, env.srv)
	viewer.login("bob", testPasswords["bob"])
	rec := viewer.get("/api/migration/" + d.ID + "/executions")
	if rec.Code != http.StatusOK {
		t.Fatalf("executions = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Executions []struct {
			ExecutionType string `json:"execution_type"`
			Status        string `json:"status"`
			SQLSent       bool   `json:"sql_sent"`
			SQLExecuted   string `json:"sql_executed"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Executions) != 1 || body.Executions[0].Status != "SUCCESS" || body.Executions[0].SQLExecuted != d.SQL {
		t.Fatalf("executions = %+v, want one SUCCESS with the script", body.Executions)
	}

	stranger := newClient(t, env.srv)
	stranger.login("carol", testPasswords["carol"])
	if code := stranger.get("/api/migration/" + d.ID + "/executions").Code; code != http.StatusForbidden {
		t.Fatalf("no access: status %d, want 403", code)
	}
}
