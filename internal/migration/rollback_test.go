package migration

import (
	"context"
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
