package migration

import (
	"context"
	"errors"
	"testing"

	"dbplatform/app/internal/accesstest"
)

func TestRepositoryDraftLifecycle(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	m, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: f.dbID, Name: "  add users  ", SQL: "CREATE TABLE u (id int)", CreatedBy: f.userID})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != StatusDraft || m.Name != "add users" || m.Version != 0 || !m.ExecutedAt.IsZero() {
		t.Fatalf("new draft = %+v", m)
	}
	if m.CreatedBy != f.userID || m.CreatedByName != "engineer" || m.DatabaseID != f.dbID || m.DatabaseName == "" {
		t.Fatalf("draft is missing author or database names: %+v", m)
	}

	m, err = f.repo.UpdateDraft(ctx, m.ID, "add users v2", "CREATE TABLE u (id int PRIMARY KEY)")
	if err != nil || m.Name != "add users v2" || m.SQL != "CREATE TABLE u (id int PRIMARY KEY)" {
		t.Fatalf("UpdateDraft = %+v, %v", m, err)
	}
	if !m.UpdatedAt.After(m.CreatedAt) && !m.UpdatedAt.Equal(m.CreatedAt) {
		t.Fatalf("updated_at %v before created_at %v", m.UpdatedAt, m.CreatedAt)
	}

	if err := f.repo.DeleteDraft(ctx, m.ID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	if _, err := f.repo.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := f.repo.DeleteDraft(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteDraft = %v, want ErrNotFound", err)
	}
	if _, err := f.repo.UpdateDraft(ctx, m.ID, "x", "SELECT 1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateDraft(missing) = %v, want ErrNotFound", err)
	}
}

func TestRepositoryValidation(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	cases := []struct {
		in   NewDraft
		want error
	}{
		{NewDraft{DatabaseID: f.dbID, Name: "", SQL: "SELECT 1"}, ErrInvalidName},
		{NewDraft{DatabaseID: f.dbID, Name: "x", SQL: "  "}, ErrEmptyScript},
		{NewDraft{DatabaseID: "db-1", Name: "x", SQL: "SELECT 1"}, ErrTargetUnavailable},
	}
	for _, tc := range cases {
		if _, err := f.repo.CreateDraft(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("CreateDraft(%+v) = %v, want %v", tc.in, err, tc.want)
		}
	}
	for _, id := range []string{"", "not-a-uuid", "00000000-0000-0000-0000-00000000ffff"} {
		if _, err := f.repo.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestRepositoryListByDatabase(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()

	other := accesstest.InsertTarget(t, f.platform, "other-db")
	if _, err := f.repo.CreateDraft(ctx, NewDraft{DatabaseID: other, Name: "elsewhere", SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}

	ok := f.draft(t, "ok", "CREATE TABLE a (x int)")
	failed := f.draft(t, "failed", "SELECT * FROM missing")
	draft := f.draft(t, "draft", "CREATE TABLE b (x int)")
	for _, d := range []*Migration{ok, failed} {
		if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
			t.Fatal(err)
		}
	}

	all, err := f.repo.ListByDatabase(ctx, f.dbID)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListByDatabase = %d rows, %v; want 3 (other databases excluded)", len(all), err)
	}
	drafts, err := f.repo.ListByDatabase(ctx, f.dbID, StatusDraft)
	if err != nil || len(drafts) != 1 || drafts[0].ID != draft.ID {
		t.Fatalf("drafts = %v, %v; want only %s", drafts, err, draft.ID)
	}
	history, err := f.repo.ListByDatabase(ctx, f.dbID, StatusSuccess, StatusFailed)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d rows, %v; want 2", len(history), err)
	}
	// Newest execution first.
	if history[0].ID != failed.ID || history[1].ID != ok.ID {
		t.Fatalf("history order = [%s %s], want [failed ok]", history[0].Name, history[1].Name)
	}
	if list, err := f.repo.ListByDatabase(ctx, "not-a-uuid"); err != nil || len(list) != 0 {
		t.Fatalf("ListByDatabase(bad id) = %v, %v; want empty", list, err)
	}
}

func TestDeletingUserKeepsHistory(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "run", "CREATE TABLE k (x int)")
	if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.platform.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, f.userID); err != nil {
		t.Fatalf("deleting a user with executed migrations should be allowed: %v", err)
	}
	m, err := f.repo.Get(ctx, d.ID)
	if err != nil || m.Status != StatusSuccess || m.Version != 1 || m.CreatedBy != "" || m.ExecutedBy != "" {
		t.Fatalf("history after user removal = %+v, %v; want SUCCESS v1 with no user", m, err)
	}
	// Setting a user id (rather than clearing it) is still a change to history.
	other := insertUser(t, f.platform, "someone")
	if _, err := f.platform.ExecContext(ctx, `UPDATE migrations SET executed_by = $2 WHERE id = $1`, d.ID, other); err == nil {
		t.Fatal("rewriting executed_by on an executed migration should be blocked")
	}
}

func TestRemovingTargetRemovesItsMigrations(t *testing.T) {
	f := newFixture(t, Limits{})
	ctx := context.Background()
	d := f.draft(t, "run", "CREATE TABLE c (x int)")
	if _, _, err := f.engine.Execute(ctx, d.ID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.platform.ExecContext(ctx, `DELETE FROM target_databases WHERE id = $1`, f.dbID); err != nil {
		t.Fatalf("deleting a target with executed migrations should cascade: %v", err)
	}
	if _, err := f.repo.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after target removal = %v, want ErrNotFound", err)
	}
}
