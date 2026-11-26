package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRollbackAndForwardWorkflow(t *testing.T) {
	f := newFixture(t, DefaultLimits())
	ctx := context.Background()

	// 1. Create draft with dual scripts
	tableName := "items_" + f.dbID[:6]
	upScript := "CREATE TABLE " + tableName + " (id serial primary key, name text);"
	downScript := "DROP TABLE " + tableName + ";"

	draft, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID,
		Name:       "create_items_table",
		SQL:        upScript,
		SQLUp:      upScript,
		SQLDown:    downScript,
		CreatedBy:  f.userID,
	})
	if err != nil {
		t.Fatalf("CreateDraft failed: %v", err)
	}

	// 2. Execute migration
	executed, res, err := f.engine.Execute(ctx, draft.ID, f.userID)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.OK {
		t.Fatalf("Execute not ok: %v", res.Err)
	}
	if executed.Status != StatusSuccess {
		t.Fatalf("expected status SUCCESS, got %s", executed.Status)
	}
	if !f.targetHasTable(t, tableName) {
		t.Fatalf("table %s should exist on target database", tableName)
	}

	// 3. Rollback the migration
	rolledBackOrig, rbRes, err := f.engine.ExecuteRollback(ctx, executed.ID, f.userID)
	if err != nil {
		t.Fatalf("ExecuteRollback failed: %v", err)
	}
	if !rbRes.OK {
		t.Fatalf("Rollback not ok: %v", rbRes.Err)
	}
	if rolledBackOrig.Status != StatusRolledBack {
		t.Fatalf("expected status ROLLED_BACK on original migration, got %s", rolledBackOrig.Status)
	}
	if f.targetHasTable(t, tableName) {
		t.Fatalf("table %s should have been dropped on target after rollback", tableName)
	}

	// Verify history contains the rollback row
	history, err := f.repo.ListByDatabase(ctx, f.dbID)
	if err != nil {
		t.Fatalf("ListByDatabase failed: %v", err)
	}

	var foundRollbackRow bool
	for _, h := range history {
		if h.ExecutionType == "ROLLBACK" && h.RollbackTargetID == executed.ID {
			foundRollbackRow = true
			if h.Status != StatusSuccess {
				t.Errorf("rollback row status = %s, want SUCCESS", h.Status)
			}
			if !strings.HasPrefix(h.Name, "Rollback: ") {
				t.Errorf("rollback row name = %s, want prefix Rollback: ", h.Name)
			}
		}
	}
	if !foundRollbackRow {
		t.Fatalf("expected rollback row in database history")
	}

	// 4. Forward (re-apply) the rolled-back migration
	forwarded, fwdRes, err := f.engine.ExecuteForward(ctx, rolledBackOrig.ID, f.userID)
	if err != nil {
		t.Fatalf("ExecuteForward failed: %v", err)
	}
	if !fwdRes.OK {
		t.Fatalf("Forward not ok: %v", fwdRes.Err)
	}
	if forwarded.Status != StatusSuccess {
		t.Fatalf("expected status SUCCESS after forward, got %s", forwarded.Status)
	}
	if !f.targetHasTable(t, tableName) {
		t.Fatalf("table %s should exist again after forward", tableName)
	}
}

func TestIrreversibleMigrationCannotRollback(t *testing.T) {
	f := newFixture(t, DefaultLimits())
	ctx := context.Background()

	tableName := "audit_" + f.dbID[:6]
	upScript := "CREATE TABLE " + tableName + " (id serial primary key);"

	draft, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID:         f.dbID,
		Name:               "irreversible_audit",
		SQL:                upScript,
		SQLUp:              upScript,
		IsIrreversible:     true,
		IrreversibleReason: "data audit tidak boleh dihapus kembali",
		CreatedBy:          f.userID,
	})
	if err != nil {
		t.Fatalf("CreateDraft failed: %v", err)
	}

	executed, res, err := f.engine.Execute(ctx, draft.ID, f.userID)
	if err != nil || !res.OK {
		t.Fatalf("Execute failed: %v, res: %v", err, res)
	}

	// Rollback should fail
	_, _, err = f.engine.ExecuteRollback(ctx, executed.ID, f.userID)
	if err == nil {
		t.Fatalf("expected rollback on irreversible migration to fail, but it succeeded")
	}
	if !strings.Contains(err.Error(), "irreversible") {
		t.Errorf("expected error to mention irreversible, got: %v", err)
	}
}

func TestRollbackLegacyMigrationWithoutSQLDown(t *testing.T) {
	f := newFixture(t, DefaultLimits())
	ctx := context.Background()

	tableName := "legacy_" + f.dbID[:6]
	upScript := "CREATE TABLE " + tableName + " (id int);"

	// Simulate a legacy migration executed before dual-scripts existed (SQLDown is empty)
	draft, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID,
		Name:       "legacy_table",
		SQL:        upScript,
		SQLUp:      upScript,
		SQLDown:    "",
		CreatedBy:  f.userID,
	})
	if err != nil {
		t.Fatalf("CreateDraft failed: %v", err)
	}

	executed, res, err := f.engine.Execute(ctx, draft.ID, f.userID)
	if err != nil || !res.OK {
		t.Fatalf("Execute failed: %v", err)
	}
	if !f.targetHasTable(t, tableName) {
		t.Fatalf("table %s should exist on target", tableName)
	}

	// Rollback should infer DROP TABLE and succeed
	rolledBack, rbRes, err := f.engine.ExecuteRollback(ctx, executed.ID, f.userID)
	if err != nil {
		t.Fatalf("ExecuteRollback failed on legacy migration: %v", err)
	}
	if !rbRes.OK {
		t.Fatalf("Rollback result not ok: %v", rbRes.Err)
	}
	if rolledBack.Status != StatusRolledBack {
		t.Fatalf("expected status ROLLED_BACK, got %s", rolledBack.Status)
	}
	if f.targetHasTable(t, tableName) {
		t.Fatalf("table %s should have been dropped by inferred rollback", tableName)
	}
}

func TestLIFORollbackAndLinearForwardOrder(t *testing.T) {
	f := newFixture(t, DefaultLimits())
	ctx := context.Background()

	execMig := func(name, up, down string) *Migration {
		d, err := f.repo.CreateDraft(ctx, NewDraft{
			DatabaseID: f.dbID,
			Name:       name,
			SQL:        up,
			SQLUp:      up,
			SQLDown:    down,
			CreatedBy:  f.userID,
		})
		if err != nil {
			t.Fatalf("CreateDraft(%s): %v", name, err)
		}
		ex, res, err := f.engine.Execute(ctx, d.ID, f.userID)
		if err != nil || !res.OK {
			t.Fatalf("Execute(%s): err=%v, res=%+v", name, err, res)
		}
		return ex
	}

	mA := execMig("A", "CREATE TABLE lifo_a (id int);", "DROP TABLE lifo_a;")
	mB := execMig("B", "CREATE TABLE lifo_b (id int);", "DROP TABLE lifo_b;")
	mC := execMig("C", "CREATE TABLE lifo_c (id int);", "DROP TABLE lifo_c;")

	// LIFO: Trying to rollback A or B while C is still active must fail
	if _, _, err := f.engine.ExecuteRollback(ctx, mA.ID, f.userID); !errors.Is(err, ErrRollbackNotLatest) {
		t.Fatalf("rollback A while C active = %v, want ErrRollbackNotLatest", err)
	}
	if _, _, err := f.engine.ExecuteRollback(ctx, mB.ID, f.userID); !errors.Is(err, ErrRollbackNotLatest) {
		t.Fatalf("rollback B while C active = %v, want ErrRollbackNotLatest", err)
	}

	// Rollback C -> succeeds
	rbC, res, err := f.engine.ExecuteRollback(ctx, mC.ID, f.userID)
	if err != nil || !res.OK || rbC.Status != StatusRolledBack {
		t.Fatalf("rollback C = %v, status=%s", err, rbC.Status)
	}

	// Now B is latest active -> rollback B succeeds
	rbB, res, err := f.engine.ExecuteRollback(ctx, mB.ID, f.userID)
	if err != nil || !res.OK || rbB.Status != StatusRolledBack {
		t.Fatalf("rollback B = %v, status=%s", err, rbB.Status)
	}

	// Linear Forward: Trying to forward C before B must fail
	if _, _, err := f.engine.ExecuteForward(ctx, mC.ID, f.userID); !errors.Is(err, ErrForwardNotLinear) {
		t.Fatalf("forward C before B = %v, want ErrForwardNotLinear", err)
	}

	// Forward B first -> succeeds (append-only new row E4)
	fwdB, res, err := f.engine.ExecuteForward(ctx, mB.ID, f.userID)
	if err != nil || !res.OK || fwdB.Status != StatusSuccess || fwdB.SourceMigrationID != mB.ID {
		t.Fatalf("forward B failed: err=%v, fwd=%+v", err, fwdB)
	}
	if !f.targetHasTable(t, "lifo_b") {
		t.Fatalf("lifo_b must exist after forward B")
	}

	// Re-forwarding mB directly must fail (already forwarded)
	if _, _, err := f.engine.ExecuteForward(ctx, mB.ID, f.userID); !errors.Is(err, ErrAlreadyForwarded) {
		t.Fatalf("double forward B = %v, want ErrAlreadyForwarded", err)
	}

	// Now Forward C -> succeeds
	fwdC, res, err := f.engine.ExecuteForward(ctx, mC.ID, f.userID)
	if err != nil || !res.OK || fwdC.Status != StatusSuccess || fwdC.SourceMigrationID != mC.ID {
		t.Fatalf("forward C failed: err=%v, fwd=%+v", err, fwdC)
	}
	if !f.targetHasTable(t, "lifo_c") {
		t.Fatalf("lifo_c must exist after forward C")
	}
}

func TestFailedRollbackDoesNotPolluteHistory(t *testing.T) {
	f := newFixture(t, DefaultLimits())
	ctx := context.Background()

	d, err := f.repo.CreateDraft(ctx, NewDraft{
		DatabaseID: f.dbID,
		Name:       "bad_rollback",
		SQL:        "CREATE TABLE real_tbl (id int);",
		SQLUp:      "CREATE TABLE real_tbl (id int);",
		SQLDown:    "DROP TABLE nonexistent_tbl_999;",
		CreatedBy:  f.userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ex, res, err := f.engine.Execute(ctx, d.ID, f.userID)
	if err != nil || !res.OK {
		t.Fatal(err)
	}

	// Attempt rollback -> SQL down fails on target
	_, rbRes, err := f.engine.ExecuteRollback(ctx, ex.ID, f.userID)
	if err == nil || rbRes == nil || rbRes.OK {
		t.Fatalf("expected rollback failure, got err=%v, res=%+v", err, rbRes)
	}

	// Original migration must stay SUCCESS
	cur, err := f.repo.Get(ctx, ex.ID)
	if err != nil || cur.Status != StatusSuccess {
		t.Fatalf("expected original migration to remain SUCCESS after failed rollback, got %s (%v)", cur.Status, err)
	}

	// History must NOT contain any FAILED rollback entry
	history, err := f.repo.ListByDatabase(ctx, f.dbID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("expected exactly 1 row in history (no failed rollback recorded), got %d", len(history))
	}
}
