package migration

import (
	"context"
	"strings"
	"testing"

	"dbplatform/internal/accesstest"
)

// approvedDraft is a migration both leaders approved, ready to be queued.
func (e *approvalEnv) approvedDraft(t *testing.T) *Migration {
	t.Helper()
	ctx := context.Background()
	d, err := e.repo.CreateDraft(ctx, NewDraft{DatabaseID: e.dbID, Name: "orders", SQL: "CREATE TABLE orders (id int)", SQLDown: "DROP TABLE orders", CreatedBy: e.userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitReview(ctx, e.userID, d.ID); err != nil {
		t.Fatal(err)
	}
	for _, leader := range []string{e.userID, e.lead2} {
		if _, err := e.svc.Approve(ctx, leader, d.ID, "ok"); err != nil {
			t.Fatal(err)
		}
	}
	if m := e.migration(t, d.ID); m.Status != StatusApproved {
		t.Fatalf("draft = %s, want APPROVED", m.Status)
	}
	return d
}

func (e *approvalEnv) approvalStatus(t *testing.T, migrationID string) map[string]string {
	t.Helper()
	list, err := e.repo.ListApprovals(context.Background(), migrationID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range list {
		out[a.GroupName] = a.Status
	}
	return out
}

func TestRunWaitsForAGroupThatGotAccessAfterTheApproval(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	d := e.approvedDraft(t)
	_, job, err := e.svc.EnqueueExecute(ctx, e.userID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	// While the job waits in the queue, a third group gets access (and the sync hook is not
	// called, as if the grant came from somewhere else).
	e.group(t, "Late", "VIEW_ONLY", insertUser(t, e.platform, "late"))

	if _, err := fastWorker(e.fixture).RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if j := e.job(t, job.ID); j.Status != JobFailed || !strings.Contains(j.LastError, "Late") {
		t.Fatalf("job = %s (%s), want FAILED naming the group Late", j.Status, j.LastError)
	}
	if e.targetHasTable(t, "orders") {
		t.Fatal("the migration ran on the target")
	}
	m := e.migration(t, d.ID)
	if m.Status != StatusPendingReview {
		t.Fatalf("migration = %s, want PENDING_REVIEW for the new group", m.Status)
	}
	if got := e.approvalStatus(t, d.ID); got["Editors"] != "APPROVED" || got["Analysts"] != "APPROVED" || got["Late"] != "PENDING" {
		t.Fatalf("approvals = %v, want the old ones kept and Late PENDING", got)
	}
	ex := e.executions(t, d.ID)
	if len(ex) != 1 || ex[0].SQLSent || ex[0].Status != ExecFailed {
		t.Fatalf("executions = %+v, want one FAILED attempt with sql_sent = false", ex)
	}
}

func TestRunIsRefusedWhenTheExecutorLostEditAccess(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	d := e.approvedDraft(t)
	_, job, err := e.svc.EnqueueExecute(ctx, e.userID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	accesstest.Grant(t, e.platform, e.editors, e.dbID, "VIEW_ONLY") // the executor's only group

	if _, err := fastWorker(e.fixture).RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if j := e.job(t, job.ID); j.Status != JobFailed || !strings.Contains(j.LastError, "VIEW_EDIT") {
		t.Fatalf("job = %s (%s), want FAILED: the executor lost VIEW_EDIT", j.Status, j.LastError)
	}
	if e.targetHasTable(t, "orders") {
		t.Fatal("the migration ran on the target")
	}
	if m := e.migration(t, d.ID); m.Status != StatusApproved {
		t.Fatalf("migration = %s, want APPROVED again (another editor may run it)", m.Status)
	}
}

func TestResumeIsRefusedWhenAGroupGotAccessAfterTheRequest(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)
	_, req, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	if err != nil {
		t.Fatal(err)
	}
	for _, leader := range []string{e.userID, e.lead2} {
		if _, err := e.svc.ApproveBatchAction(ctx, leader, req.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	if err != nil || job == nil {
		t.Fatalf("run approved resume = %v, %v", job, err)
	}
	e.group(t, "Late", "VIEW_ONLY", insertUser(t, e.platform, "late"))
	if _, err := fastWorker(e.fixture).RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if j := e.job(t, job.ID); j.Status != JobFailed || !strings.Contains(j.LastError, "Late") {
		t.Fatalf("resume job = %s (%s), want FAILED naming Late", j.Status, j.LastError)
	}
	if got := e.migration(t, m.ID); got.Status != StatusPartial || got.BatchesUp != 1 {
		t.Fatalf("migration = %s up %d, want PARTIAL with 1 batch (nothing ran)", got.Status, got.BatchesUp)
	}
}

func TestSyncApprovalsFollowsGrantChanges(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	analysts := ""
	d, err := e.repo.CreateDraft(ctx, NewDraft{DatabaseID: e.dbID, Name: "orders", SQL: "CREATE TABLE orders (id int)", SQLDown: "DROP TABLE orders", CreatedBy: e.userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitReview(ctx, e.userID, d.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := e.repo.ListApprovals(ctx, d.ID)
	for _, a := range list {
		if a.GroupName == "Analysts" {
			analysts = a.GroupID
		}
	}
	if _, err := e.svc.Approve(ctx, e.userID, d.ID, "ok"); err != nil {
		t.Fatal(err)
	}

	// Analysts loses access: its pending approval no longer blocks the review.
	if _, err := e.platform.Exec(`DELETE FROM group_db_grants WHERE group_id = $1`, analysts); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SyncApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if m := e.migration(t, d.ID); m.Status != StatusApproved {
		t.Fatalf("after Analysts lost access = %s, want APPROVED", m.Status)
	}
	if got := e.approvalStatus(t, d.ID); len(got) != 1 || got["Editors"] != "APPROVED" {
		t.Fatalf("approvals = %v, want only Editors APPROVED", got)
	}

	// A new group gets access: the approved migration goes back to review for it.
	e.group(t, "Late", "VIEW_EDIT", insertUser(t, e.platform, "late"))
	if err := e.svc.SyncApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if m := e.migration(t, d.ID); m.Status != StatusPendingReview {
		t.Fatalf("after Late got access = %s, want PENDING_REVIEW", m.Status)
	}
	if got := e.approvalStatus(t, d.ID); got["Late"] != "PENDING" || got["Editors"] != "APPROVED" {
		t.Fatalf("approvals = %v, want Late PENDING and Editors kept", got)
	}
}

func TestSyncApprovalsFollowsGrantChangesOnBatchRequests(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)
	_, req, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApproveBatchAction(ctx, e.userID, req.ID, ""); err != nil {
		t.Fatal(err)
	}
	// Analysts (still PENDING) loses access: the request is approved by the groups left.
	if _, err := e.platform.Exec(`DELETE FROM group_db_grants g USING groups gr WHERE g.group_id = gr.id AND gr.name = 'Analysts'`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SyncApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.repo.GetBatchRequest(ctx, req.ID)
	if err != nil || got.Status != BatchRequestApproved || len(got.Approvals) != 1 {
		t.Fatalf("request = %+v, %v; want APPROVED with one approval", got, err)
	}

	e.group(t, "Late", "VIEW_ONLY", insertUser(t, e.platform, "late"))
	if err := e.svc.SyncApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.repo.GetBatchRequest(ctx, req.ID); got.Status != BatchRequestPending || len(got.Approvals) != 2 {
		t.Fatalf("request = %+v, want PENDING again with Late's approval added", got)
	}
}
