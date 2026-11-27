package groups_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/authn"
	"dbplatform/internal/groups"
	"dbplatform/internal/testutil"
)

type leaderEnv struct {
	db    *sql.DB
	store *authn.SessionStore
	repo  *groups.Repository
}

func newLeaderEnv(t *testing.T) *leaderEnv {
	t.Helper()
	db := testutil.NewPlatformDB(t)
	return &leaderEnv{db: db, store: authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig()), repo: groups.NewRepository(db)}
}

func (e *leaderEnv) user(t *testing.T, name, role string) string {
	t.Helper()
	u, err := e.store.RegisterUser(context.Background(), authn.RegisterInput{Username: name, Password: "Password-" + name, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func (e *leaderEnv) deactivate(t *testing.T, userID string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE users SET is_active = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
}

func (e *leaderEnv) leader(t *testing.T, groupID string) string {
	t.Helper()
	l, err := e.repo.GetLeader(context.Background(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestPublicCanHaveAnActiveMemberAsLeader_Integration(t *testing.T) {
	e := newLeaderEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice", authn.RoleUser)
	bob := e.user(t, "bob", authn.RoleUser)
	e.deactivate(t, bob)

	if err := e.repo.SetLeader(ctx, groups.PublicGroupID, alice); err != nil {
		t.Fatalf("Public leader = %v, want allowed", err)
	}
	if got := e.leader(t, groups.PublicGroupID); got != alice {
		t.Fatalf("Public leader = %q, want alice", got)
	}
	if err := e.repo.SetLeader(ctx, groups.PublicGroupID, bob); !errors.Is(err, groups.ErrLeaderInactive) {
		t.Fatalf("inactive leader = %v, want ErrLeaderInactive", err)
	}
	g, _ := e.repo.Create(ctx, "Ops", "")
	if err := e.repo.SetLeader(ctx, g.ID, alice); !errors.Is(err, groups.ErrLeaderNotMember) {
		t.Fatalf("non-member leader = %v, want ErrLeaderNotMember", err)
	}
}

func TestLeaderCannotBeClearedWhileTheGroupHasAccess_Integration(t *testing.T) {
	e := newLeaderEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice", authn.RoleUser)
	g, _ := e.repo.Create(ctx, "Ops", "")
	_ = e.repo.AddMember(ctx, g.ID, alice)
	accesstest.Grant(t, e.db, g.ID, accesstest.InsertTarget(t, e.db, "prod"), "VIEW_ONLY")

	// The only member is inactive: nobody can take over, so the leader cannot be cleared.
	e.deactivate(t, alice)
	if err := e.repo.SetLeader(ctx, g.ID, ""); !errors.Is(err, groups.ErrLeaderRequired) {
		t.Fatalf("clear leader of a group with access = %v, want ErrLeaderRequired", err)
	}
	if ok, _ := e.repo.HasActiveLeader(ctx, g.ID); ok {
		t.Fatal("an inactive leader must not count as active")
	}
	list, err := e.repo.LeaderlessWithGrants(ctx)
	if err != nil || len(list) != 1 || list[0].ID != g.ID {
		t.Fatalf("leaderless with grants = %+v, %v; want Ops", list, err)
	}
}

func TestLeadershipSkipsInactiveMembers_Integration(t *testing.T) {
	e := newLeaderEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice", authn.RoleUser)
	bob := e.user(t, "bob", authn.RoleUser)
	carol := e.user(t, "carol", authn.RoleUser)
	g, _ := e.repo.Create(ctx, "Ops", "")
	for _, u := range []string{alice, bob, carol} {
		_ = e.repo.AddMember(ctx, g.ID, u)
	}
	e.deactivate(t, bob)

	// Alice leaves: bob is next in line but inactive, so carol leads.
	if err := e.repo.RemoveMember(ctx, g.ID, alice); err != nil {
		t.Fatal(err)
	}
	if got := e.leader(t, g.ID); got != carol {
		t.Fatalf("leader after alice left = %q, want carol", got)
	}
	// Carol is deactivated: nobody active is left.
	e.deactivate(t, carol)
	left, err := e.repo.HandOverLeadership(ctx, carol)
	if err != nil || len(left) != 1 || left[0].ID != g.ID {
		t.Fatalf("hand over = %+v, %v; want Ops left without a leader", left, err)
	}
	if got := e.leader(t, g.ID); got != "" {
		t.Fatalf("leader = %q, want none", got)
	}
	// A new active member takes the vacant seat.
	dave := e.user(t, "dave", authn.RoleUser)
	_ = e.repo.AddMember(ctx, g.ID, dave)
	if got := e.leader(t, g.ID); got != dave {
		t.Fatalf("leader after dave joined = %q, want dave", got)
	}
}

func TestPublicLeaderDefaultsToTheBootstrapAdmin_Integration(t *testing.T) {
	e := newLeaderEnv(t)
	ctx := context.Background()
	first := e.user(t, "root", authn.RolePlatformAdmin)
	second := e.user(t, "admin2", authn.RolePlatformAdmin)

	got, err := e.repo.EnsurePublicLeader(ctx, second)
	if err != nil || got != second || e.leader(t, groups.PublicGroupID) != second {
		t.Fatalf("EnsurePublicLeader(preferred) = %q, %v; want the preferred admin", got, err)
	}
	if got, _ := e.repo.EnsurePublicLeader(ctx, first); got != "" {
		t.Fatalf("Public already has an active leader; EnsurePublicLeader changed it to %q", got)
	}
	// The Public leader is deactivated: the earliest active Platform Admin takes over.
	e.deactivate(t, second)
	if _, err := e.repo.HandOverLeadership(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got := e.leader(t, groups.PublicGroupID); got != first {
		t.Fatalf("Public leader after hand over = %q, want the other admin", got)
	}
	// A preferred account that is inactive falls back to the earliest active admin.
	e.deactivate(t, first)
	third := e.user(t, "admin3", authn.RolePlatformAdmin)
	if got, err := e.repo.EnsurePublicLeader(ctx, first); err != nil || got != third {
		t.Fatalf("EnsurePublicLeader(inactive preferred) = %q, %v; want admin3", got, err)
	}
}
