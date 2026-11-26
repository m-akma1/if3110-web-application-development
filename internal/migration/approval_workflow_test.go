package migration_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	"dbplatform/internal/ids"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"
	"dbplatform/internal/testutil"
)

func TestMigrationApprovalWorkflow_Robustness_Integration(t *testing.T) {
	platformDB := testutil.NewPlatformDB(t)
	targetDB := testutil.NewSchemaDB(t)
	ctx := context.Background()

	// 1. Setup Target DB and Pools
	dbID := accesstest.InsertTarget(t, platformDB, "prod-db-"+ids.New()[:6])
	pools := dbmanager.NewConnectionRegistry()
	pools.RegisterPool(dbID, targetDB)

	// 2. Setup Stores and Services
	sessConfig := authn.DefaultSessionConfig()
	sessionStore := authn.NewPostgresSessionStore(platformDB, sessConfig)
	groupRepo := groups.NewRepository(platformDB)
	grantStore := rbac.NewPostgresGrantStore(platformDB)
	authorizer := rbac.NewAuthorizer(grantStore)

	engine := migration.NewEngine(platformDB, pools, migration.Limits{})
	repo := migration.NewRepository(platformDB)
	svc := migration.NewService(repo, engine, authorizer, grantStore)

	// 3. Register Users:
	// Alice (Leader of Backend)
	// Bob (Leader of DevOps)
	// Charlie (Developer / author, regular member)
	// David (Outsider)
	alice, err := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "alice", Password: "PasswordAlice1"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "bob", Password: "PasswordBob123"})
	if err != nil {
		t.Fatal(err)
	}
	charlie, err := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "charlie", Password: "PasswordChar1"})
	if err != nil {
		t.Fatal(err)
	}
	david, err := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "david", Password: "PasswordDave1"})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Create Groups & Add Members
	// Backend group
	gBackend, err := groupRepo.Create(ctx, "Backend", "Backend team")
	if err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.AddMember(ctx, gBackend.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.AddMember(ctx, gBackend.ID, charlie.ID); err != nil {
		t.Fatal(err)
	}
	// DevOps group
	gDevops, err := groupRepo.Create(ctx, "DevOps", "DevOps team")
	if err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.AddMember(ctx, gDevops.ID, bob.ID); err != nil {
		t.Fatal(err)
	}

	// Verify leaders:
	// Alice is leader of Backend (first added)
	lBackend, err := groupRepo.GetLeader(ctx, gBackend.ID)
	if err != nil || lBackend != alice.ID {
		t.Fatalf("expected alice to be leader of Backend, got %q", lBackend)
	}
	// Bob is leader of DevOps
	lDevops, err := groupRepo.GetLeader(ctx, gDevops.ID)
	if err != nil || lDevops != bob.ID {
		t.Fatalf("expected bob to be leader of DevOps, got %q", lDevops)
	}

	// 5. Grant database permissions to both groups (VIEW_EDIT)
	if _, err := grantStore.SetGroupGrant(ctx, gBackend.ID, dbID, rbac.AccessViewEdit); err != nil {
		t.Fatal(err)
	}
	if _, err := grantStore.SetGroupGrant(ctx, gDevops.ID, dbID, rbac.AccessViewEdit); err != nil {
		t.Fatal(err)
	}

	// 6. Charlie creates a draft
	draft, err := svc.CreateDraft(ctx, charlie.ID, dbID, "tambah_tabel_produk", "CREATE TABLE products (id int PRIMARY KEY, name text);")
	if err != nil {
		t.Fatalf("CreateDraft failed: %v", err)
	}
	if draft.Status != migration.StatusDraft {
		t.Fatalf("draft initial status should be DRAFT, got %q", draft.Status)
	}

	// 7. Security Check: Charlie attempts to execute prematurely -> MUST FAIL
	if _, _, err := svc.Execute(ctx, charlie.ID, draft.ID); !errors.Is(err, migration.ErrNotApproved) {
		t.Fatalf("executing unapproved draft must fail with ErrNotApproved, got %v", err)
	}

	// 8. Charlie submits for review
	reviewed, err := svc.SubmitReview(ctx, charlie.ID, draft.ID)
	if err != nil {
		t.Fatalf("SubmitReview failed: %v", err)
	}
	if reviewed.Status != migration.StatusPendingReview {
		t.Fatalf("status after submit review should be PENDING_REVIEW, got %q", reviewed.Status)
	}

	// 9. Security Check: Charlie attempts to execute while PENDING_REVIEW -> MUST FAIL
	if _, _, err := svc.Execute(ctx, charlie.ID, draft.ID); !errors.Is(err, migration.ErrNotApproved) {
		t.Fatalf("executing draft in PENDING_REVIEW must fail with ErrNotApproved, got %v", err)
	}

	// 10. Security Check: Unauthorized approval / rejection blocked
	// Charlie is not a leader of any group -> MUST FAIL
	if _, err := svc.Approve(ctx, charlie.ID, draft.ID, "unauthorized"); !errors.Is(err, migration.ErrNotLeader) {
		t.Fatalf("approval by non-leader must fail with ErrNotLeader, got %v", err)
	}
	if _, err := svc.Reject(ctx, charlie.ID, draft.ID, "unauthorized"); !errors.Is(err, migration.ErrNotLeader) {
		t.Fatalf("rejection by non-leader must fail with ErrNotLeader, got %v", err)
	}
	// David is an outsider with no access to database -> MUST FAIL with ErrForbidden
	if _, err := svc.Approve(ctx, david.ID, draft.ID, "outsider"); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatalf("approval by outsider must fail with ErrForbidden, got %v", err)
	}

	// 11. Alice (Leader of Backend) approves
	appAlice, err := svc.Approve(ctx, alice.ID, draft.ID, "Backend approve ok")
	if err != nil {
		t.Fatalf("Alice approval failed: %v", err)
	}
	// Status MUST STILL BE PENDING_REVIEW because DevOps (Bob) has not approved!
	if appAlice.Status != migration.StatusPendingReview {
		t.Fatalf("status should still be PENDING_REVIEW (waiting for Bob), got %q", appAlice.Status)
	}

	// 12. Security Check: Alice tries to approve a second time -> MUST FAIL (no pending groups left)
	if _, err := svc.Approve(ctx, alice.ID, draft.ID, "second time"); !errors.Is(err, migration.ErrNotLeader) && !errors.Is(err, migration.ErrAlreadyReviewed) {
		t.Fatalf("duplicate approval must fail with ErrNotLeader or ErrAlreadyReviewed, got %v", err)
	}

	// 13. Bob (Leader of DevOps) rejects
	rejBob, err := svc.Reject(ctx, bob.ID, draft.ID, "Perlu tambah kolom price")
	if err != nil {
		t.Fatalf("Bob rejection failed: %v", err)
	}
	if rejBob.Status != migration.StatusRejected {
		t.Fatalf("status after rejection should be REJECTED, got %q", rejBob.Status)
	}
	if rejBob.RejectionReason != "Perlu tambah kolom price" {
		t.Fatalf("unexpected rejection reason: %q", rejBob.RejectionReason)
	}

	// 14. Charlie attempts to execute REJECTED migration -> MUST FAIL
	if _, _, err := svc.Execute(ctx, charlie.ID, draft.ID); !errors.Is(err, migration.ErrNotApproved) {
		t.Fatalf("executing REJECTED migration must fail with ErrNotApproved, got %v", err)
	}

	// 15. Charlie revises the draft
	revisedSQL := "CREATE TABLE products (id int PRIMARY KEY, name text, price numeric);"
	revised, err := svc.SaveDraft(ctx, charlie.ID, draft.ID, "tambah_tabel_produk_v2", revisedSQL)
	if err != nil {
		t.Fatalf("SaveDraft on rejected migration failed: %v", err)
	}
	if revised.Status != migration.StatusDraft {
		t.Fatalf("status after editing rejected migration should reset to DRAFT, got %q", revised.Status)
	}

	// 16. Charlie re-submits for review
	reSubmitted, err := svc.SubmitReview(ctx, charlie.ID, draft.ID)
	if err != nil {
		t.Fatalf("Re-submit review failed: %v", err)
	}
	if reSubmitted.Status != migration.StatusPendingReview {
		t.Fatalf("status should be PENDING_REVIEW, got %q", reSubmitted.Status)
	}

	// 17. Alice approves revised draft
	appAlice2, err := svc.Approve(ctx, alice.ID, draft.ID, "Backend approved v2")
	if err != nil {
		t.Fatalf("Alice approval 2 failed: %v", err)
	}
	if appAlice2.Status != migration.StatusPendingReview {
		t.Fatalf("status should still be PENDING_REVIEW after 1 of 2 approves, got %q", appAlice2.Status)
	}

	// 18. Bob approves revised draft -> ALL LEADERS HAVE APPROVED!
	appBob2, err := svc.Approve(ctx, bob.ID, draft.ID, "DevOps approved v2")
	if err != nil {
		t.Fatalf("Bob approval 2 failed: %v", err)
	}
	// Migration status MUST automatically transition to APPROVED!
	if appBob2.Status != migration.StatusApproved {
		t.Fatalf("status should automatically be APPROVED when all groups approve, got %q", appBob2.Status)
	}

	// 19. Charlie (or authorized executor) runs DryRun on APPROVED migration -> Should succeed
	_, dryRunRes, err := svc.DryRun(ctx, charlie.ID, draft.ID)
	if err != nil || !dryRunRes.OK {
		t.Fatalf("DryRun on APPROVED migration should succeed: %v, %+v", err, dryRunRes)
	}

	// 20. Charlie executes the APPROVED migration -> MUST SUCCEED
	executed, runRes, err := svc.Execute(ctx, charlie.ID, draft.ID)
	if err != nil {
		t.Fatalf("Execute on APPROVED migration failed: %v", err)
	}
	if !runRes.OK {
		t.Fatalf("Execute run result not OK: %+v", runRes)
	}
	if executed.Status != migration.StatusSuccess {
		t.Fatalf("executed status should be SUCCESS, got %q", executed.Status)
	}
	if executed.Version != 1 {
		t.Fatalf("executed version should be 1, got %d", executed.Version)
	}

	// 21. Check Target DB: Table products must physically exist!
	var tableExists bool
	err = targetDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'products')").Scan(&tableExists)
	if err != nil || !tableExists {
		t.Fatalf("table products should exist in target DB, err: %v, exists: %v", err, tableExists)
	}

	// 22. Immutability & Re-execution Protection:
	// A) Cannot execute again
	if _, _, err := svc.Execute(ctx, charlie.ID, draft.ID); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("re-executing completed migration must fail with ErrNotDraft, got %v", err)
	}
	// B) Cannot edit draft
	if _, err := svc.SaveDraft(ctx, charlie.ID, draft.ID, "tambah_tabel_produk_v3", "DROP TABLE products;"); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("editing executed migration must fail with ErrNotDraft, got %v", err)
	}
	// C) Cannot re-submit review
	if _, err := svc.SubmitReview(ctx, charlie.ID, draft.ID); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("submitting review on executed migration must fail with ErrNotDraft, got %v", err)
	}

	// 23. Overview Verification:
	// The migration is now in History and no longer in Drafts!
	ov, err := svc.Overview(ctx, charlie.ID, dbID)
	if err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	if len(ov.History) != 1 || ov.History[0].ID != draft.ID {
		t.Fatalf("history should contain the executed migration: %+v", ov.History)
	}
	for _, d := range ov.Drafts {
		if d.ID == draft.ID {
			t.Fatalf("executed migration should no longer be listed in drafts")
		}
	}
}

func TestMigrationApproval_LeaderSuccessionDuringReview_Integration(t *testing.T) {
	platformDB := testutil.NewPlatformDB(t)
	targetDB := testutil.NewSchemaDB(t)
	ctx := context.Background()

	dbID := accesstest.InsertTarget(t, platformDB, "succession-db-"+ids.New()[:6])
	pools := dbmanager.NewConnectionRegistry()
	pools.RegisterPool(dbID, targetDB)

	sessionStore := authn.NewPostgresSessionStore(platformDB, authn.DefaultSessionConfig())
	groupRepo := groups.NewRepository(platformDB)
	grantStore := rbac.NewPostgresGrantStore(platformDB)
	authorizer := rbac.NewAuthorizer(grantStore)

	engine := migration.NewEngine(platformDB, pools, migration.Limits{})
	repo := migration.NewRepository(platformDB)
	svc := migration.NewService(repo, engine, authorizer, grantStore)

	// Users: Alice (initial leader), Charlie (member), Dave (author)
	alice, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "alice_s", Password: "Password12345"})
	charlie, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "charlie_s", Password: "Password12345"})
	dave, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "dave_s", Password: "Password12345"})

	g, err := groupRepo.Create(ctx, "InfraTeam", "Infra")
	if err != nil {
		t.Fatal(err)
	}
	// Alice added first -> Leader
	if err := groupRepo.AddMember(ctx, g.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	// Charlie added second -> Member
	if err := groupRepo.AddMember(ctx, g.ID, charlie.ID); err != nil {
		t.Fatal(err)
	}
	// Dave added third -> Author
	if err := groupRepo.AddMember(ctx, g.ID, dave.ID); err != nil {
		t.Fatal(err)
	}

	leader, err := groupRepo.GetLeader(ctx, g.ID)
	if err != nil || leader != alice.ID {
		t.Fatalf("Alice should be the leader, got %q", leader)
	}

	if _, err := grantStore.SetGroupGrant(ctx, g.ID, dbID, rbac.AccessViewEdit); err != nil {
		t.Fatal(err)
	}

	// Dave creates draft & submits review
	draft, err := svc.CreateDraft(ctx, dave.ID, dbID, "audit_log_table", "CREATE TABLE audit_logs (id int);")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitReview(ctx, dave.ID, draft.ID); err != nil {
		t.Fatal(err)
	}

	// Dynamic Leader Succession:
	// Alice leaves / is removed from InfraTeam
	if err := groupRepo.RemoveMember(ctx, g.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	// Verify succession: Charlie joined earliest among remaining members -> Charlie is now Leader!
	newLeader, err := groupRepo.GetLeader(ctx, g.ID)
	if err != nil || newLeader != charlie.ID {
		t.Fatalf("Charlie should have succeeded to Leader, got %q", newLeader)
	}

	// Alice tries to approve as former leader -> MUST FAIL (access revoked)
	if _, err := svc.Approve(ctx, alice.ID, draft.ID, "former leader approve"); !errors.Is(err, rbac.ErrForbidden) && !errors.Is(err, migration.ErrNotLeader) {
		t.Fatalf("former leader must not be able to approve, got %v", err)
	}

	// Charlie (new Leader) approves -> MUST SUCCEED
	app, err := svc.Approve(ctx, charlie.ID, draft.ID, "Charlie approved as new leader")
	if err != nil {
		t.Fatalf("new leader approval failed: %v", err)
	}
	if app.Status != migration.StatusApproved {
		t.Fatalf("migration should be APPROVED after new leader approved, got %q", app.Status)
	}

	// Execute approved migration
	execM, res, err := svc.Execute(ctx, dave.ID, draft.ID)
	if err != nil || !res.OK || execM.Status != migration.StatusSuccess {
		t.Fatalf("Execute failed: %v, res: %+v", err, res)
	}
}

func TestRollbackAndForwardRequireGroupApproval_Integration(t *testing.T) {
	platformDB := testutil.NewPlatformDB(t)
	targetDB := testutil.NewSchemaDB(t)
	ctx := context.Background()

	dbID := accesstest.InsertTarget(t, platformDB, "rb-approval-db-"+ids.New()[:6])
	pools := dbmanager.NewConnectionRegistry()
	pools.RegisterPool(dbID, targetDB)

	sessionStore := authn.NewPostgresSessionStore(platformDB, authn.DefaultSessionConfig())
	groupRepo := groups.NewRepository(platformDB)
	grantStore := rbac.NewPostgresGrantStore(platformDB)
	authorizer := rbac.NewAuthorizer(grantStore)
	engine := migration.NewEngine(platformDB, pools, migration.Limits{})
	repo := migration.NewRepository(platformDB)
	svc := migration.NewService(repo, engine, authorizer, grantStore)

	alice, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "alice_v", Password: "Password123"})
	bob, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "bob_e", Password: "Password123"})
	charlie, _ := sessionStore.RegisterUser(ctx, authn.RegisterInput{Username: "charlie_dev", Password: "Password123"})

	viewGroup, _ := groupRepo.Create(ctx, "ViewTeam", "View-only stakeholder")
	editGroup, _ := groupRepo.Create(ctx, "EditTeam", "Dev team")

	// Alice leads ViewTeam (VIEW_ONLY), Bob leads EditTeam (VIEW_EDIT), Charlie is dev member of EditTeam
	_ = groupRepo.AddMember(ctx, viewGroup.ID, alice.ID)
	_ = groupRepo.AddMember(ctx, editGroup.ID, bob.ID)
	_ = groupRepo.AddMember(ctx, editGroup.ID, charlie.ID)

	_, _ = grantStore.SetGroupGrant(ctx, viewGroup.ID, dbID, rbac.AccessViewOnly)
	_, _ = grantStore.SetGroupGrant(ctx, editGroup.ID, dbID, rbac.AccessViewEdit)

	// 1. Initial migration (E2)
	draft, err := svc.CreateDraftFull(ctx, migration.NewDraft{
		DatabaseID: dbID,
		Name:       "tbl_events",
		SQL:        "CREATE TABLE events (id int);",
		SQLUp:      "CREATE TABLE events (id int);",
		SQLDown:    "DROP TABLE events;",
		CreatedBy:  charlie.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitReview(ctx, charlie.ID, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, alice.ID, draft.ID, "ok view"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, bob.ID, draft.ID, "ok edit"); err != nil {
		t.Fatal(err)
	}
	execM, res, err := svc.Execute(ctx, charlie.ID, draft.ID)
	if err != nil || !res.OK || execM.Status != migration.StatusSuccess {
		t.Fatalf("Execute initial migration failed: err=%v, res=%+v", err, res)
	}

	// 2. Charlie requests Rollback -> must enter PENDING_REVIEW (not execute immediately!)
	rbDraft, rbRes, err := svc.Rollback(ctx, charlie.ID, execM.ID)
	if err != nil {
		t.Fatalf("Rollback request failed: %v", err)
	}
	if rbRes != nil {
		t.Fatalf("expected rbRes to be nil because group approvals are required")
	}
	if rbDraft.Status != migration.StatusPendingReview || rbDraft.ExecutionType != "ROLLBACK" || rbDraft.RollbackTargetID != execM.ID {
		t.Fatalf("unexpected rbDraft state: %+v", rbDraft)
	}

	// Duplicate rollback request while pending must be rejected
	if _, _, err := svc.Rollback(ctx, charlie.ID, execM.ID); !errors.Is(err, migration.ErrPendingRollbackExists) {
		t.Fatalf("duplicate rollback = %v, want ErrPendingRollbackExists", err)
	}

	// Editing immutable rollback script must fail
	if _, err := svc.SaveDraftFull(ctx, charlie.ID, rbDraft.ID, migration.UpdateDraftInput{Name: "tamper", SQL: "DROP TABLE something_else;"}); !errors.Is(err, migration.ErrImmutableRollbackScript) {
		t.Fatalf("editing rollback draft = %v, want ErrImmutableRollbackScript", err)
	}

	// Executing unapproved rollback must fail
	if _, _, err := svc.Execute(ctx, charlie.ID, rbDraft.ID); !errors.Is(err, migration.ErrNotApproved) {
		t.Fatalf("execute unapproved rollback = %v, want ErrNotApproved", err)
	}

	// Alice (VIEW_ONLY leader) approves rollback -> still PENDING_REVIEW
	step1, err := svc.Approve(ctx, alice.ID, rbDraft.ID, "alice ok rollback")
	if err != nil || step1.Status != migration.StatusPendingReview {
		t.Fatalf("Alice approval = %+v, err=%v", step1, err)
	}

	// Bob (VIEW_EDIT leader) approves rollback -> becomes APPROVED
	step2, err := svc.Approve(ctx, bob.ID, rbDraft.ID, "bob ok rollback")
	if err != nil || step2.Status != migration.StatusApproved {
		t.Fatalf("Bob approval = %+v, err=%v", step2, err)
	}

	// Charlie executes approved rollback -> drops table, E2 becomes ROLLED_BACK, E3 becomes SUCCESS
	rbExec, rbRunRes, err := svc.Execute(ctx, charlie.ID, rbDraft.ID)
	if err != nil || rbRunRes == nil || !rbRunRes.OK || rbExec.Status != migration.StatusSuccess {
		t.Fatalf("Execute approved rollback failed: err=%v, res=%+v, m=%+v", err, rbRunRes, rbExec)
	}

	// Check E2 status
	e2Updated, _, err := svc.Get(ctx, charlie.ID, execM.ID)
	if err != nil || e2Updated.Status != migration.StatusRolledBack {
		t.Fatalf("expected E2 status ROLLED_BACK, got %s (%v)", e2Updated.Status, err)
	}

	// 3. Charlie requests Forward -> must enter PENDING_REVIEW
	fwdDraft, fwdRes, err := svc.Forward(ctx, charlie.ID, execM.ID)
	if err != nil {
		t.Fatalf("Forward request failed: %v", err)
	}
	if fwdRes != nil {
		t.Fatalf("expected fwdRes nil while pending review")
	}
	if fwdDraft.Status != migration.StatusPendingReview || fwdDraft.ExecutionType != "FORWARD" || fwdDraft.SourceMigrationID != execM.ID {
		t.Fatalf("unexpected fwdDraft: %+v", fwdDraft)
	}

	if _, err := svc.Approve(ctx, alice.ID, fwdDraft.ID, "alice ok forward"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, bob.ID, fwdDraft.ID, "bob ok forward"); err != nil {
		t.Fatal(err)
	}

	fwdExec, fwdRunRes, err := svc.Execute(ctx, charlie.ID, fwdDraft.ID)
	if err != nil || !fwdRunRes.OK || fwdExec.Status != migration.StatusSuccess || fwdExec.SourceMigrationID != execM.ID {
		t.Fatalf("Execute approved forward failed: err=%v, fwd=%+v", err, fwdExec)
	}

	// Verify append-only: E2 stays ROLLED_BACK, E3 stays ROLLBACK SUCCESS, E4 is FORWARD SUCCESS!
	ov, err := svc.Overview(ctx, charlie.ID, dbID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.History) != 3 {
		t.Fatalf("expected 3 append-only history rows (E2, E3, E4), got %d", len(ov.History))
	}
}
