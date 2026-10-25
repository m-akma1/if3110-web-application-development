package rbac_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/accesstest"
	"dbplatform/internal/authn"
	"dbplatform/internal/groups"
	"dbplatform/internal/rbac"
	"dbplatform/internal/testutil"
)

func TestAuthorizer_Postgres_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	gr := groups.NewRepository(db)
	az := rbac.NewAuthorizer(rbac.NewPostgresGrantStore(db))

	alice, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "alice", Password: "PasswordAlice1"})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "boss", Password: "PasswordBoss12", Role: authn.RolePlatformAdmin})
	if err != nil {
		t.Fatal(err)
	}

	shared := accesstest.InsertTarget(t, db, "shared")
	prod := accesstest.InsertTarget(t, db, "prod")
	engineers, err := gr.Create(ctx, "Engineers", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := gr.AddMember(ctx, engineers.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	// Public grant reaches everyone; Engineers grant upgrades alice on the same DB.
	accesstest.Grant(t, db, groups.PublicGroupID, shared, "VIEW_ONLY")
	accesstest.Grant(t, db, engineers.ID, shared, "VIEW_EDIT")
	accesstest.Grant(t, db, engineers.ID, prod, "VIEW_ONLY")

	if lvl, err := az.Authorize(ctx, alice.ID, shared, rbac.ActionExecuteMigration); err != nil || lvl != rbac.AccessViewEdit {
		t.Fatalf("alice seharusnya VIEW_EDIT pada shared (maks Public+Engineers): %s, %v", lvl, err)
	}
	if _, err := az.Authorize(ctx, alice.ID, prod, rbac.ActionExecuteMigration); err == nil {
		t.Fatalf("alice hanya VIEW_ONLY pada prod, eksekusi migrasi harus ditolak")
	}

	// Admin: only Public membership, so only the Public grant applies. Role gives nothing extra.
	if lvl, _ := az.EffectiveLevel(ctx, admin.ID, shared); lvl != rbac.AccessViewOnly {
		t.Fatalf("admin hanya mendapat akses lewat group (Public: VIEW_ONLY), dapat %s", lvl)
	}
	if _, err := az.Authorize(ctx, admin.ID, prod, rbac.ActionViewSchema); err == nil {
		t.Fatalf("Platform Admin tanpa grant group tidak boleh melihat prod")
	}

	list, err := az.AccessibleDatabases(ctx, alice.ID)
	if err != nil || len(list) != 2 || list[0].DatabaseName != "prod" || list[1].Level != rbac.AccessViewEdit {
		t.Fatalf("daftar database alice salah: %+v, %v", list, err)
	}

	// Removing the target cascades its grants away.
	if _, err := db.ExecContext(ctx, `DELETE FROM target_databases WHERE id = $1`, shared); err != nil {
		t.Fatal(err)
	}
	if lvl, _ := az.EffectiveLevel(ctx, alice.ID, shared); lvl != rbac.AccessNone {
		t.Fatalf("grant ke database yang dihapus seharusnya hilang, dapat %s", lvl)
	}
}

func TestRaises(t *testing.T) {
	cases := []struct {
		old, new rbac.AccessLevel
		want     bool
	}{
		{rbac.AccessNone, rbac.AccessViewOnly, true},
		{rbac.AccessViewOnly, rbac.AccessViewEdit, true},
		{rbac.AccessViewEdit, rbac.AccessViewOnly, false},
		{rbac.AccessViewOnly, rbac.AccessViewOnly, false},
		{rbac.AccessViewOnly, rbac.AccessNone, false},
	}
	for _, c := range cases {
		if got := rbac.Raises(c.old, c.new); got != c.want {
			t.Errorf("Raises(%s, %s) = %v", c.old, c.new, got)
		}
	}
}

func TestSetGroupGrant_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := rbac.NewPostgresGrantStore(db)
	gr := groups.NewRepository(db)
	g, _ := gr.Create(ctx, "Ops", "")
	target := accesstest.InsertTarget(t, db, "ops-db")

	prev, err := store.SetGroupGrant(ctx, g.ID, target, rbac.AccessViewOnly)
	if err != nil || prev != rbac.AccessNone {
		t.Fatalf("grant pertama: prev=%s err=%v", prev, err)
	}
	prev, _ = store.SetGroupGrant(ctx, g.ID, target, rbac.AccessViewEdit)
	if prev != rbac.AccessViewOnly {
		t.Fatalf("level sebelumnya seharusnya VIEW_ONLY, dapat %s", prev)
	}
	all, _ := store.ListGrants(ctx)
	if len(all) != 1 || all[0].Level != rbac.AccessViewEdit {
		t.Fatalf("ListGrants salah: %+v", all)
	}
	prev, _ = store.SetGroupGrant(ctx, g.ID, target, rbac.AccessNone)
	if all, _ := store.ListGrants(ctx); prev != rbac.AccessViewEdit || len(all) != 0 {
		t.Fatalf("NONE seharusnya menghapus grant: prev=%s, sisa=%+v", prev, all)
	}
	if _, err := store.SetGroupGrant(ctx, g.ID, "00000000-0000-0000-0000-00000000beef", rbac.AccessViewOnly); !errors.Is(err, rbac.ErrUnknownGrantTarget) {
		t.Fatalf("database tidak dikenal seharusnya ErrUnknownGrantTarget, dapat %v", err)
	}
	if _, err := store.SetGroupGrant(ctx, "bukan-uuid", target, rbac.AccessViewOnly); !errors.Is(err, rbac.ErrUnknownGrantTarget) {
		t.Fatalf("group id tidak valid seharusnya ErrUnknownGrantTarget, dapat %v", err)
	}
}
