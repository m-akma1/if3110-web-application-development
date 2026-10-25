package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"dbplatform/app/internal/rbac"
)

// stubAccess gives each user a fixed level on every database, using the real rbac rules.
type stubAccess map[string]rbac.AccessLevel

func (s stubAccess) Authorize(_ context.Context, userID, _ string, action rbac.Action) (rbac.AccessLevel, error) {
	level := rbac.Normalize(s[userID])
	if !rbac.IsAllowed(level, action) {
		return level, rbac.ErrForbidden
	}
	return level, nil
}

func newServiceFixture(t *testing.T) (*fixture, *Service, string, string) {
	t.Helper()
	f := newFixture(t, Limits{})
	viewer := insertUser(t, f.platform, "viewer")
	outsider := insertUser(t, f.platform, "outsider")
	svc := NewService(f.repo, f.engine, stubAccess{f.userID: rbac.AccessViewEdit, viewer: rbac.AccessViewOnly})
	return f, svc, viewer, outsider
}

func TestServiceEditorFlow(t *testing.T) {
	f, svc, _, _ := newServiceFixture(t)
	ctx := context.Background()
	editor := f.userID

	d, err := svc.CreateDraft(ctx, editor, f.dbID, "add t", "CREATE TABLE t (x int);\nINSERT INTO missing VALUES (1);")
	if err != nil {
		t.Fatal(err)
	}
	if _, res, err := svc.DryRun(ctx, editor, d.ID); err != nil || res.OK || res.Err.Statement != 2 {
		t.Fatalf("DryRun = %+v, %v; want failure at statement 2", res, err)
	}
	failed, res, err := svc.Execute(ctx, editor, d.ID)
	if err != nil || res.OK || failed.Status != StatusFailed {
		t.Fatalf("Execute = %+v, %v; want FAILED", failed, err)
	}
	if _, _, err := svc.DryRun(ctx, editor, d.ID); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("DryRun of an executed migration = %v, want ErrNotDraft", err)
	}

	copied, err := svc.Duplicate(ctx, editor, failed.ID)
	if err != nil || copied.Status != StatusDraft || copied.SQL != failed.SQL || copied.Name != "add t (salinan)" {
		t.Fatalf("Duplicate = %+v, %v", copied, err)
	}
	fixed, err := svc.SaveDraft(ctx, editor, copied.ID, "add t", "CREATE TABLE t (x int);")
	if err != nil {
		t.Fatal(err)
	}
	done, _, err := svc.Execute(ctx, editor, fixed.ID)
	if err != nil || done.Status != StatusSuccess || done.Version != 1 {
		t.Fatalf("Execute fixed = %+v, %v; want SUCCESS v1", done, err)
	}

	scratch, _ := svc.CreateDraft(ctx, editor, f.dbID, "scratch", "SELECT 1")
	ov, err := svc.Overview(ctx, editor, f.dbID)
	if err != nil || !ov.CanEdit || len(ov.History) != 2 || len(ov.Drafts) != 1 || ov.Drafts[0].ID != scratch.ID {
		t.Fatalf("Overview = %+v, %v; want 2 history rows and 1 draft", ov, err)
	}
	if deleted, err := svc.DeleteDraft(ctx, editor, scratch.ID); err != nil || deleted.ID != scratch.ID {
		t.Fatalf("DeleteDraft = %+v, %v", deleted, err)
	}
}

func TestServiceViewOnlySeesHistoryOnly(t *testing.T) {
	f, svc, viewer, _ := newServiceFixture(t)
	ctx := context.Background()
	editor := f.userID

	draft, _ := svc.CreateDraft(ctx, editor, f.dbID, "draft", "CREATE TABLE a (x int)")
	runID, _ := svc.CreateDraft(ctx, editor, f.dbID, "run", "CREATE TABLE b (x int)")
	run, _, err := svc.Execute(ctx, editor, runID.ID)
	if err != nil {
		t.Fatal(err)
	}

	ov, err := svc.Overview(ctx, viewer, f.dbID)
	if err != nil || ov.CanEdit || ov.Level != rbac.AccessViewOnly || len(ov.History) != 1 || len(ov.Drafts) != 0 {
		t.Fatalf("viewer Overview = %+v, %v; want history only", ov, err)
	}
	if m, level, err := svc.Get(ctx, viewer, run.ID); err != nil || m.ID != run.ID || level != rbac.AccessViewOnly {
		t.Fatalf("viewer Get(executed) = %v, %v", level, err)
	}

	denied := map[string]error{}
	_, _, denied["get draft"] = svc.Get(ctx, viewer, draft.ID)
	_, denied["create"] = svc.CreateDraft(ctx, viewer, f.dbID, "x", "SELECT 1")
	_, denied["save"] = svc.SaveDraft(ctx, viewer, draft.ID, "x", "SELECT 1")
	_, denied["delete"] = svc.DeleteDraft(ctx, viewer, draft.ID)
	_, denied["duplicate"] = svc.Duplicate(ctx, viewer, run.ID)
	_, _, denied["dry run"] = svc.DryRun(ctx, viewer, draft.ID)
	_, _, denied["execute"] = svc.Execute(ctx, viewer, draft.ID)
	for what, err := range denied {
		if !errors.Is(err, rbac.ErrForbidden) {
			t.Errorf("viewer %s = %v, want rbac.ErrForbidden", what, err)
		}
	}
	if m, _ := f.repo.Get(ctx, draft.ID); m.Status != StatusDraft || m.Name != "draft" {
		t.Fatalf("viewer calls must not change the draft: %+v", m)
	}
}

func TestServiceNoAccessAndUnknownIDsLookTheSame(t *testing.T) {
	f, svc, _, outsider := newServiceFixture(t)
	ctx := context.Background()
	d, _ := svc.CreateDraft(ctx, f.userID, f.dbID, "d", "SELECT 1")

	if _, err := svc.Overview(ctx, outsider, f.dbID); !errors.Is(err, rbac.ErrForbidden) {
		t.Errorf("outsider Overview = %v, want ErrForbidden", err)
	}
	for _, id := range []string{d.ID, "00000000-0000-0000-0000-00000000ffff", "garbage"} {
		if _, _, err := svc.Get(ctx, outsider, id); !errors.Is(err, rbac.ErrForbidden) {
			t.Errorf("outsider Get(%s) = %v, want ErrForbidden", id, err)
		}
	}
	// Even an editor gets ErrForbidden (not ErrNotFound) for an id that does not exist.
	if _, _, err := svc.Execute(ctx, f.userID, "00000000-0000-0000-0000-00000000ffff"); !errors.Is(err, rbac.ErrForbidden) {
		t.Errorf("editor Execute(unknown) = %v, want ErrForbidden", err)
	}
}

func TestCopyName(t *testing.T) {
	if got := copyName("  add index "); got != "add index (salinan)" {
		t.Errorf("copyName = %q", got)
	}
	long := copyName(strings.Repeat("é", MaxNameLength))
	if n := utf8.RuneCountInString(long); n != MaxNameLength || !strings.HasSuffix(long, copySuffix) {
		t.Errorf("long copy has %d characters (%q...), want %d ending in the suffix", n, long[:10], MaxNameLength)
	}
	if _, _, err := NormalizeDraft(long, "SELECT 1"); err != nil {
		t.Errorf("copied name must stay valid: %v", err)
	}
}
