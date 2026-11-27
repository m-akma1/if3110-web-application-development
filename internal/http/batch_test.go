package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/migration"
)

func TestBatchMigrationEndpoints_Integration(t *testing.T) {
	var logs bytes.Buffer
	env := newTestEnv(t, &logs)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "batch")
	pool := env.connectTarget(t, target)
	if _, err := pool.Exec(`CREATE TABLE items (id bigint PRIMARY KEY, v int NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO items (id) SELECT g FROM generate_series(1, 300) g`); err != nil {
		t.Fatal(err)
	}
	editors, _ := env.groups.Create(ctx, "Editors", "")
	bob := userID(t, env.srv, "bob")
	_ = env.groups.AddMember(ctx, editors.ID, bob)
	accesstest.Grant(t, env.db, editors.ID, target, "VIEW_EDIT")
	c := newClient(t, env.srv)
	c.login("bob", testPasswords["bob"])

	// The batch settings sent by the editor are stored on the draft.
	rec := c.postJSON("/api/migration/drafts", `{"db_id":"`+target+`","name":"bump","sql_up":"UPDATE items SET v = v + 1",
		"sql_down":"UPDATE items SET v = v - 1","execution_mode":"BATCH","batch_attribute":"id","batch_size":100}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create batch draft = %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Draft struct {
			ID             string `json:"id"`
			ExecutionMode  string `json:"execution_mode"`
			BatchAttribute string `json:"batch_attribute"`
			BatchSize      int    `json:"batch_size"`
		} `json:"draft"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	d := created.Draft
	if d.ExecutionMode != "BATCH" || d.BatchAttribute != "id" || d.BatchSize != 100 {
		t.Fatalf("draft = %+v, want BATCH id/100", d)
	}
	// A script that cannot run per batch is refused when the draft is saved.
	if rec := c.postJSON("/api/migration/drafts", `{"db_id":"`+target+`","name":"bad","sql_up":"INSERT INTO items VALUES (1)",
		"execution_mode":"BATCH","batch_attribute":"id","batch_size":100}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("insert as batch = %d %s, want 422", rec.Code, rec.Body)
	}
	// Resume and abort only apply to PARTIAL migrations.
	if rec := c.post("/api/migration/"+d.ID+"/resume", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("resume a draft = %d, want 422", rec.Code)
	}

	// Run it until batch 2 fails: PARTIAL after 1 batch.
	if _, err := pool.Exec(`ALTER TABLE items ADD CONSTRAINT stop_here CHECK (id <> 150 OR v = 0)`); err != nil {
		t.Fatal(err)
	}
	if rec := c.post("/api/migration/"+d.ID+"/execute", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("execute = %d %s", rec.Code, rec.Body)
	}
	engine := migration.NewEngine(env.db, env.registry, migration.Limits{})
	w := migration.NewWorker(env.migrations, engine, migration.WorkerConfig{PollInterval: 10 * time.Millisecond})
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Migration struct {
			Status    string `json:"status"`
			Committed int    `json:"batch_committed_count"`
			Total     int    `json:"batch_total_count"`
			Cursor    *int64 `json:"checkpoint_cursor"`
		} `json:"migration"`
	}
	_ = json.Unmarshal(c.get("/api/migration/"+d.ID).Body.Bytes(), &detail)
	if m := detail.Migration; m.Status != "PARTIAL" || m.Committed != 1 || m.Total != 3 || m.Cursor == nil || *m.Cursor != 100 {
		t.Fatalf("detail = %+v, want PARTIAL 1/3 cursor 100", m)
	}
	var batches struct {
		Batches []struct {
			Direction string `json:"direction"`
			CursorTo  int64  `json:"cursor_to"`
		} `json:"batches"`
	}
	_ = json.Unmarshal(c.get("/api/migration/"+d.ID+"/batches").Body.Bytes(), &batches)
	if len(batches.Batches) != 1 || batches.Batches[0].CursorTo != 100 {
		t.Fatalf("batches = %+v, want one UP batch up to 100", batches.Batches)
	}

	// Stop without rollback: ABORTED at once, the committed batch stays.
	rec = c.postJSON("/api/migration/"+d.ID+"/abort", `{"rollback":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("abort = %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(c.get("/api/migration/"+d.ID).Body.Bytes(), &detail)
	if detail.Migration.Status != "ABORTED" {
		t.Fatalf("after abort status = %s, want ABORTED", detail.Migration.Status)
	}
	var changed int
	if err := pool.QueryRow(`SELECT count(*) FROM items WHERE v = 1`).Scan(&changed); err != nil || changed != 100 {
		t.Fatalf("rows changed = %d, %v; want 100 kept", changed, err)
	}
	found := false
	for _, e := range auditEvents(t, &logs) {
		if e["event"] == "audit.abort" && e["outcome"] == "success" && e["migration_id"] == d.ID && e["actor"] == bob {
			found = true
		}
	}
	if !found {
		t.Fatal("no audit.abort event for the abort")
	}
}

// With leader approvals on, resume of a PARTIAL batch migration is a request that runs only
// after the leader of every group with access approved it.
func TestBatchResumeNeedsLeaderApproval_Integration(t *testing.T) {
	var logs bytes.Buffer
	env := newApprovalTestEnv(t, &logs)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "batch-approval")
	pool := env.connectTarget(t, target)
	if _, err := pool.Exec(`CREATE TABLE items (id bigint PRIMARY KEY, v int NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`INSERT INTO items (id) SELECT g FROM generate_series(1, 300) g`); err != nil {
		t.Fatal(err)
	}
	// bob leads Editors (VIEW_EDIT), carol leads Analysts (VIEW_ONLY).
	bob, carol := userID(t, env.srv, "bob"), userID(t, env.srv, "carol")
	editors, _ := env.groups.Create(ctx, "Editors", "")
	analysts, _ := env.groups.Create(ctx, "Analysts", "")
	_ = env.groups.AddMember(ctx, editors.ID, bob)
	_ = env.groups.AddMember(ctx, analysts.ID, carol)
	accesstest.Grant(t, env.db, editors.ID, target, "VIEW_EDIT")
	accesstest.Grant(t, env.db, analysts.ID, target, "VIEW_ONLY")
	bobC, carolC := newClient(t, env.srv), newClient(t, env.srv)
	bobC.login("bob", testPasswords["bob"])
	carolC.login("carol", testPasswords["carol"])

	rec := bobC.postJSON("/api/migration/drafts", `{"db_id":"`+target+`","name":"bump","sql_up":"UPDATE items SET v = v + 1",
		"sql_down":"UPDATE items SET v = v - 1","execution_mode":"BATCH","batch_attribute":"id","batch_size":100}`)
	var created struct {
		Draft struct {
			ID string `json:"id"`
		} `json:"draft"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created.Draft.ID
	if rec := bobC.post("/api/migration/"+id+"/submit-review", nil); rec.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	for _, c := range []*client{bobC, carolC} {
		if rec := c.post("/api/migration/"+id+"/approve", nil); rec.Code != http.StatusOK {
			t.Fatalf("approve = %d %s", rec.Code, rec.Body)
		}
	}
	if _, err := pool.Exec(`ALTER TABLE items ADD CONSTRAINT stop_here CHECK (id <> 150 OR v = 0)`); err != nil {
		t.Fatal(err)
	}
	if rec := bobC.post("/api/migration/"+id+"/execute", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("execute = %d %s", rec.Code, rec.Body)
	}
	w := migration.NewWorker(env.migrations, migration.NewEngine(env.db, env.registry, migration.Limits{}),
		migration.WorkerConfig{PollInterval: 10 * time.Millisecond})
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}

	// Resume opens a request instead of running.
	rec = bobC.post("/api/migration/"+id+"/resume", nil)
	var asked struct {
		RequiresApproval bool `json:"requires_approval"`
		BatchRequest     struct {
			ID        string `json:"id"`
			Action    string `json:"action"`
			Status    string `json:"status"`
			Approvals []any  `json:"approvals"`
		} `json:"batch_request"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &asked)
	req := asked.BatchRequest
	if rec.Code != http.StatusOK || !asked.RequiresApproval || req.Action != "RESUME" || req.Status != "PENDING" || len(req.Approvals) != 2 {
		t.Fatalf("resume = %d %s, want a PENDING RESUME request with 2 approvals", rec.Code, rec.Body)
	}
	if rec := bobC.post("/api/migration/"+id+"/resume", nil); rec.Code != http.StatusConflict {
		t.Fatalf("resume while the request waits = %d, want 409", rec.Code)
	}

	// carol sees it in her review queue and can approve it.
	var queue struct {
		BatchRequests []struct {
			ID         string `json:"id"`
			CanApprove bool   `json:"can_approve"`
		} `json:"batch_requests"`
	}
	_ = json.Unmarshal(carolC.get("/api/migration/pending-reviews").Body.Bytes(), &queue)
	if len(queue.BatchRequests) != 1 || queue.BatchRequests[0].ID != req.ID || !queue.BatchRequests[0].CanApprove {
		t.Fatalf("carol's queue = %+v, want the request with can_approve", queue.BatchRequests)
	}
	if rec := carolC.postJSON("/api/batch-requests/"+req.ID+"/reject", `{"reason":""}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reject without a reason = %d, want 422", rec.Code)
	}
	for _, c := range []*client{bobC, carolC} {
		if rec := c.postJSON("/api/batch-requests/"+req.ID+"/approve", `{"note":"ok"}`); rec.Code != http.StatusOK {
			t.Fatalf("approve request = %d %s", rec.Code, rec.Body)
		}
	}
	// carol (VIEW_ONLY) may approve but not run it.
	if rec := carolC.post("/api/migration/"+id+"/resume", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("resume by a viewer = %d, want 403", rec.Code)
	}
	rec = bobC.post("/api/migration/"+id+"/resume", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run approved resume = %d %s, want 202", rec.Code, rec.Body)
	}
	if _, err := w.RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Migration struct {
			Status       string `json:"status"`
			BatchRequest struct {
				Status string `json:"status"`
				JobID  string `json:"job_id"`
			} `json:"batch_request"`
		} `json:"migration"`
	}
	_ = json.Unmarshal(bobC.get("/api/migration/"+id).Body.Bytes(), &detail)
	if m := detail.Migration; m.Status != "SUCCESS" || m.BatchRequest.Status != "DONE" || m.BatchRequest.JobID == "" {
		t.Fatalf("detail = %+v, want SUCCESS with a DONE request", m)
	}

	var sawRequest, sawApprove bool
	for _, e := range auditEvents(t, &logs) {
		if e["batch_request_id"] != req.ID {
			continue
		}
		sawRequest = sawRequest || (e["event"] == "audit.enqueue" && e["requires_approval"] == true && e["request"] == "resume")
		sawApprove = sawApprove || (e["event"] == "audit.approve" && e["actor"] == carol)
	}
	if !sawRequest || !sawApprove {
		t.Fatalf("audit events for the request: request %v, approve %v", sawRequest, sawApprove)
	}
}

// Removing a group's grant through the admin API resyncs open reviews: the group's pending
// approval goes away and a review that only waited for it becomes APPROVED.
func TestGrantChangeResyncsOpenReviews_Integration(t *testing.T) {
	env := newApprovalTestEnv(t, io.Discard)
	ctx := context.Background()
	target := accesstest.InsertTarget(t, env.db, "grant-sync")
	bob, carol := userID(t, env.srv, "bob"), userID(t, env.srv, "carol")
	editors, _ := env.groups.Create(ctx, "Editors", "")
	analysts, _ := env.groups.Create(ctx, "Analysts", "")
	_ = env.groups.AddMember(ctx, editors.ID, bob)
	_ = env.groups.AddMember(ctx, analysts.ID, carol)
	accesstest.Grant(t, env.db, editors.ID, target, "VIEW_EDIT")
	accesstest.Grant(t, env.db, analysts.ID, target, "VIEW_ONLY")

	d, err := env.migrations.CreateDraft(ctx, migration.NewDraft{DatabaseID: target, Name: "orders", SQL: "CREATE TABLE orders (id int)", SQLDown: "DROP TABLE orders", CreatedBy: bob})
	if err != nil {
		t.Fatal(err)
	}
	bobC := newClient(t, env.srv)
	bobC.login("bob", testPasswords["bob"])
	if rec := bobC.post("/api/migration/"+d.ID+"/submit-review", nil); rec.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	if rec := bobC.post("/api/migration/"+d.ID+"/approve", nil); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}

	admin := newClient(t, env.srv)
	admin.login("admin", testPasswords["admin"])
	if rec := admin.postJSON("/api/admin/grants",
		`{"group_id":"`+analysts.ID+`","database_id":"`+target+`","level":"NONE"}`); rec.Code != http.StatusOK {
		t.Fatalf("remove grant = %d %s", rec.Code, rec.Body)
	}
	if m, _ := env.migrations.Get(ctx, d.ID); m.Status != migration.StatusApproved {
		t.Fatalf("after Analysts lost access = %s, want APPROVED", m.Status)
	}
}
