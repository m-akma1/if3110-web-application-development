package migration

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/groups"
	"dbplatform/internal/rbac"
)

// approvalEnv is a fixture with the approval rules on: the editor (f.userID) leads Editors
// (VIEW_EDIT) and lead2 leads Analysts (VIEW_ONLY), so every change needs both leaders.
type approvalEnv struct {
	*fixture
	svc     *Service
	groups  *groups.Repository
	editors string
	lead2   string
	member2 string // in Analysts, not its leader
}

func newApprovalEnv(t *testing.T) *approvalEnv {
	t.Helper()
	f := newFixture(t, Limits{})
	grants := rbac.NewPostgresGrantStore(f.platform)
	e := &approvalEnv{
		fixture: f,
		svc:     NewService(f.repo, f.engine, rbac.NewAuthorizer(grants), grants),
		groups:  groups.NewRepository(f.platform),
		lead2:   insertUser(t, f.platform, "lead2"),
		member2: insertUser(t, f.platform, "member2"),
	}
	e.editors = e.group(t, "Editors", "VIEW_EDIT", f.userID)
	e.group(t, "Analysts", "VIEW_ONLY", e.lead2, e.member2)
	return e
}

// group creates a group whose first member becomes its leader, with level on the database.
func (e *approvalEnv) group(t *testing.T, name, level string, members ...string) string {
	t.Helper()
	ctx := context.Background()
	g, err := e.groups.Create(ctx, name, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range members {
		if err := e.groups.AddMember(ctx, g.ID, uid); err != nil {
			t.Fatal(err)
		}
	}
	accesstest.Grant(t, e.platform, g.ID, e.dbID, level)
	return g.ID
}

// partialBatch returns a batch migration that stopped PARTIAL after batch 1 of 3, with the
// failing constraint removed so it can resume.
func (e *approvalEnv) partialBatch(t *testing.T) *Migration {
	t.Helper()
	e.items(t, 2500)
	m := e.batchDraft(t, 1000)
	e.approveAll(t, m.ID) // the first run went through review
	e.partial(t, m, 1500)
	if _, err := e.target.Exec(`ALTER TABLE items DROP CONSTRAINT stop_here`); err != nil {
		t.Fatal(err)
	}
	return e.migration(t, m.ID)
}

// approveAll records an APPROVED review of migrationID by every group with access, as if each
// leader had approved it.
func (e *approvalEnv) approveAll(t *testing.T, migrationID string) {
	t.Helper()
	if _, err := e.platform.Exec(`
		INSERT INTO migration_approvals (id, migration_id, group_id, status, reviewed_at)
		SELECT gen_random_uuid(), $1, g.group_id, 'APPROVED', NOW()
		FROM group_db_grants g WHERE g.database_id = $2 AND g.access_level <> 'NONE'`, migrationID, e.dbID); err != nil {
		t.Fatal(err)
	}
}

func TestResumeNeedsEveryLeaderBeforeItRuns(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)

	_, req, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	if err != nil || job != nil || req == nil || req.Status != BatchRequestPending || len(req.Approvals) != 2 {
		t.Fatalf("request = %+v, job %v, err %v; want a PENDING request with 2 approvals and no job", req, job, err)
	}
	if got := e.migration(t, m.ID); got.Status != StatusPartial {
		t.Fatalf("migration = %s, want PARTIAL until approved", got.Status)
	}
	if _, _, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortMark); !errors.Is(err, ErrBatchRequestPending) {
		t.Fatalf("second request while one waits = %v, want ErrBatchRequestPending", err)
	}
	// Only the leader of a group may approve for it.
	if _, err := e.svc.ApproveBatchAction(ctx, e.member2, req.ID, ""); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("approval by a non-leader = %v, want ErrNotLeader", err)
	}
	if got, err := e.svc.ApproveBatchAction(ctx, e.userID, req.ID, "ok"); err != nil || got.Status != BatchRequestPending {
		t.Fatalf("after one of two approvals = %+v, %v; want still PENDING", got, err)
	}
	if _, _, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume); job != nil || !errors.Is(err, ErrBatchRequestPending) {
		t.Fatalf("resume with one approval = job %v, %v; want ErrBatchRequestPending", job, err)
	}
	got, err := e.svc.ApproveBatchAction(ctx, e.lead2, req.ID, "")
	if err != nil || got.Status != BatchRequestApproved || got.DecidedAt == nil {
		t.Fatalf("after both approvals = %+v, %v; want APPROVED", got, err)
	}
	if _, err := e.svc.ApproveBatchAction(ctx, e.lead2, req.ID, ""); !errors.Is(err, ErrBatchRequestNotPending) {
		t.Fatalf("approving an approved request = %v, want ErrBatchRequestNotPending", err)
	}

	// Approved: the editor runs it.
	_, done, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	if err != nil || job == nil || job.Type != JobResume {
		t.Fatalf("run approved resume = job %+v, %v", job, err)
	}
	if done.Status != BatchRequestDone || done.JobID != job.ID {
		t.Fatalf("request after run = %s job %q, want DONE with job %s", done.Status, done.JobID, job.ID)
	}
	if _, err := fastWorker(e.fixture).RunOnce(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if got := e.migration(t, m.ID); got.Status != StatusSuccess || got.BatchesUp != 3 {
		t.Fatalf("migration = %s with %d batches, want SUCCESS 3", got.Status, got.BatchesUp)
	}
	// The detail page shows the request that ran.
	detail, _, err := e.svc.Get(ctx, e.userID, m.ID)
	if err != nil || detail.BatchRequest == nil || detail.BatchRequest.ID != req.ID {
		t.Fatalf("detail batch request = %+v, %v", detail.BatchRequest, err)
	}
}

func TestRejectedBatchActionCanBeRequestedAgain(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)

	_, req, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortMark)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RejectBatchAction(ctx, e.lead2, req.ID, "  "); !errors.Is(err, ErrRejectReasonRequired) {
		t.Fatalf("reject without a reason = %v, want ErrRejectReasonRequired", err)
	}
	got, err := e.svc.RejectBatchAction(ctx, e.lead2, req.ID, "data masih dipakai")
	if err != nil || got.Status != BatchRequestRejected || got.RejectionReason != "data masih dipakai" {
		t.Fatalf("rejected request = %+v, %v", got, err)
	}
	if got := e.migration(t, m.ID); got.Status != StatusPartial {
		t.Fatalf("migration = %s, want PARTIAL after a rejection", got.Status)
	}

	// A rejection is final for that request; a new one can be made.
	_, again, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortMark)
	if err != nil || again.ID == req.ID || again.Status != BatchRequestPending {
		t.Fatalf("new request = %+v, %v", again, err)
	}
	for _, leader := range []string{e.userID, e.lead2} {
		if _, err := e.svc.ApproveBatchAction(ctx, leader, again.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	after, done, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortMark)
	if err != nil || job != nil || after.Status != StatusAborted || done.Status != BatchRequestDone {
		t.Fatalf("run approved mark-stopped = %s, request %+v, job %v, %v; want ABORTED and DONE", after.Status, done, job, err)
	}
}

func TestApprovedRequestForAnotherActionIsCancelled(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	m := e.partialBatch(t)

	_, resume, _, _ := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume)
	for _, leader := range []string{e.userID, e.lead2} {
		if _, err := e.svc.ApproveBatchAction(ctx, leader, resume.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The editor changes their mind: an abort needs its own approval, and the unused resume
	// approval is cancelled so it cannot run later.
	_, abort, job, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchAbortRollback)
	if err != nil || job != nil || abort.Status != BatchRequestPending {
		t.Fatalf("abort request = %+v, job %v, %v", abort, job, err)
	}
	if old, _ := e.repo.GetBatchRequest(ctx, resume.ID); old.Status != BatchRequestCancelled {
		t.Fatalf("replaced resume request = %s, want CANCELLED", old.Status)
	}
	if _, _, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume); !errors.Is(err, ErrBatchRequestPending) {
		t.Fatalf("resume after cancellation = %v, want ErrBatchRequestPending (the abort waits)", err)
	}
}

func TestBatchActionNeedsEditAccess(t *testing.T) {
	e := newApprovalEnv(t)
	m := e.partialBatch(t)
	// lead2's group only has VIEW_ONLY.
	if _, _, _, err := e.svc.RequestBatchAction(context.Background(), e.lead2, m.ID, BatchResume); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatalf("resume by a viewer = %v, want ErrForbidden", err)
	}
}

func TestReviewIsRefusedWhenAGroupHasNoActiveLeader(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	if _, err := e.platform.Exec(`UPDATE users SET is_active = FALSE WHERE id = $1`, e.lead2); err != nil {
		t.Fatal(err)
	}

	d, err := e.repo.CreateDraft(ctx, NewDraft{DatabaseID: e.dbID, Name: "orders", SQL: "CREATE TABLE orders (id int)", SQLDown: "DROP TABLE orders", CreatedBy: e.userID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitReview(ctx, e.userID, d.ID); !errors.Is(err, ErrGroupWithoutLeader) {
		t.Fatalf("submit with a leaderless group = %v, want ErrGroupWithoutLeader", err)
	}
	if got := e.migration(t, d.ID); got.Status != StatusDraft {
		t.Fatalf("draft = %s, want DRAFT (no review opened)", got.Status)
	}
	m := e.partialBatch(t)
	if _, _, _, err := e.svc.RequestBatchAction(ctx, e.userID, m.ID, BatchResume); !errors.Is(err, ErrGroupWithoutLeader) {
		t.Fatalf("batch request with a leaderless group = %v, want ErrGroupWithoutLeader", err)
	}
}

// failingGrants stands in for a grant store whose database is unreachable.
type failingGrants struct{}

func (failingGrants) GroupsWithAccess(context.Context, string) ([]string, error) {
	return nil, errors.New("platform database unavailable")
}

func TestExecuteFailsClosedWhenGroupsCannotBeRead(t *testing.T) {
	e := newApprovalEnv(t)
	ctx := context.Background()
	svc := NewService(e.repo, e.engine, e.svc.access, failingGrants{})
	d := e.draft(t, "orders", "CREATE TABLE orders (id int)")

	if _, job, err := svc.EnqueueExecute(ctx, e.userID, d.ID); err == nil || job != nil {
		t.Fatalf("execute without knowing the approvers = job %v, %v; want an error", job, err)
	}
	if _, err := e.repo.LatestJobForMigration(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a job was queued: %v", err)
	}
}
