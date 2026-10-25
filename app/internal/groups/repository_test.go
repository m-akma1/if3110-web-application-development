package groups_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/testutil"
)

func TestRandomCode_Format(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := groups.RandomCode()
		if len(c) != 14 || !strings.HasPrefix(c, "JOIN-") || strings.ContainsAny(c[5:], "01IO") {
			t.Fatalf("format kode acak tidak sesuai: %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 195 {
		t.Fatalf("kode acak terlalu sering bertabrakan: %d unik dari 200", len(seen))
	}
}

func TestRepository_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	repo := groups.NewRepository(db)

	u, err := store.RegisterUser(ctx, authn.RegisterInput{Username: "member", Password: "PasswordMember1"})
	if err != nil {
		t.Fatal(err)
	}

	g, err := repo.Create(ctx, "Engineers", "tim backend")
	if err != nil {
		t.Fatalf("membuat group gagal: %v", err)
	}
	if _, err := repo.Create(ctx, "engineers", ""); !errors.Is(err, groups.ErrGroupNameTaken) {
		t.Fatalf("nama group duplikat (beda huruf) seharusnya ditolak, dapat: %v", err)
	}
	if _, err := repo.Create(ctx, "x", ""); !errors.Is(err, groups.ErrInvalidGroupName) {
		t.Fatalf("nama group terlalu pendek seharusnya ditolak, dapat: %v", err)
	}

	// Public is always there for new users
	mine, err := repo.ListForUser(ctx, u.ID)
	if err != nil || len(mine) != 1 || mine[0].ID != groups.PublicGroupID {
		t.Fatalf("user baru seharusnya hanya anggota Public: %+v, %v", mine, err)
	}

	if err := repo.AddMember(ctx, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddMember(ctx, g.ID, u.ID); err != nil {
		t.Fatalf("menambah anggota yang sudah ada seharusnya no-op: %v", err)
	}
	mine, _ = repo.ListForUser(ctx, u.ID)
	if len(mine) != 2 {
		t.Fatalf("user seharusnya anggota Public + Engineers, dapat %+v", mine)
	}

	inv, err := repo.CreateInviteCode(ctx, "eng-2026", g.ID, u.ID)
	if err != nil || inv.Code != "ENG-2026" {
		t.Fatalf("membuat kode undangan gagal: %+v, %v", inv, err)
	}
	if _, err := repo.CreateInviteCode(ctx, "ENG-2026", g.ID, ""); !errors.Is(err, groups.ErrInviteCodeTaken) {
		t.Fatalf("kode duplikat seharusnya ditolak, dapat: %v", err)
	}
	if _, err := repo.CreateInviteCode(ctx, "PUBLIC-CODE", groups.PublicGroupID, ""); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("kode undangan ke Public tidak masuk akal dan harus ditolak, dapat: %v", err)
	}
	if _, err := repo.CreateInviteCode(ctx, "a b", g.ID, ""); !errors.Is(err, groups.ErrInvalidInviteCode) {
		t.Fatalf("format kode tidak valid seharusnya ditolak, dapat: %v", err)
	}
	if random, err := repo.CreateInviteCode(ctx, "", g.ID, ""); err != nil || !strings.HasPrefix(random.Code, "JOIN-") {
		t.Fatalf("kode kosong seharusnya dibuat acak: %+v, %v", random, err)
	}

	resolved, err := repo.ResolveInviteCode(ctx, "  eng-2026 ")
	if err != nil || resolved.ID != g.ID {
		t.Fatalf("kode undangan seharusnya ditemukan (case-insensitive): %+v, %v", resolved, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE invite_codes SET is_active = FALSE WHERE code = 'ENG-2026'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveInviteCode(ctx, "ENG-2026"); !errors.Is(err, groups.ErrInvalidInviteCode) {
		t.Fatalf("kode nonaktif seharusnya tidak valid, dapat: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil || len(all) != 2 || !all[0].IsSystem || all[1].MemberCount != 1 {
		t.Fatalf("daftar group salah: %+v, %v", all, err)
	}
	byUser, err := repo.MemberGroupsByUser(ctx)
	if err != nil || len(byUser[u.ID]) != 2 {
		t.Fatalf("MemberGroupsByUser salah: %+v, %v", byUser, err)
	}
}

func TestRepository_Management_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()
	store := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	repo := groups.NewRepository(db)
	u, _ := store.RegisterUser(ctx, authn.RegisterInput{Username: "anggota", Password: "PasswordAnggota1"})

	g, _ := repo.Create(ctx, "Data", "lama")
	other, _ := repo.Create(ctx, "Other", "")
	if err := repo.Rename(ctx, g.ID, "Data Team", "baru"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, g.ID); got.Name != "Data Team" || got.Description != "baru" {
		t.Fatalf("rename tidak tersimpan: %+v", got)
	}
	if err := repo.Rename(ctx, g.ID, "other", ""); !errors.Is(err, groups.ErrGroupNameTaken) {
		t.Fatalf("rename ke nama yang dipakai seharusnya ditolak, dapat %v", err)
	}
	if err := repo.Rename(ctx, groups.PublicGroupID, "Semua", ""); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("nama Public tidak boleh diubah, dapat %v", err)
	}
	if err := repo.Rename(ctx, groups.PublicGroupID, "Public", "deskripsi baru"); err != nil {
		t.Fatalf("deskripsi Public boleh diubah: %v", err)
	}

	_ = repo.AddMember(ctx, g.ID, u.ID)
	if ids, _ := repo.MemberIDs(ctx, g.ID); len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("MemberIDs salah: %v", ids)
	}
	if err := repo.RemoveMember(ctx, groups.PublicGroupID, u.ID); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("keanggotaan Public tidak boleh dihapus, dapat %v", err)
	}
	if err := repo.RemoveMember(ctx, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := repo.MemberIDs(ctx, g.ID); len(ids) != 0 {
		t.Fatalf("anggota seharusnya sudah keluar: %v", ids)
	}

	if _, err := repo.CreateInviteCode(ctx, "DATA-CODE", g.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetInviteCodeActive(ctx, "data-code", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveInviteCode(ctx, "DATA-CODE"); !errors.Is(err, groups.ErrInvalidInviteCode) {
		t.Fatalf("kode nonaktif tidak boleh dipakai, dapat %v", err)
	}
	if err := repo.SetInviteCodeActive(ctx, "TIDAK-ADA", true); !errors.Is(err, groups.ErrInvalidInviteCode) {
		t.Fatalf("kode tidak dikenal seharusnya ditolak, dapat %v", err)
	}

	if err := repo.Delete(ctx, groups.PublicGroupID); !errors.Is(err, groups.ErrSystemGroup) {
		t.Fatalf("Public tidak boleh dihapus, dapat %v", err)
	}
	if err := repo.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveInviteCode(ctx, "DATA-CODE"); !errors.Is(err, groups.ErrInvalidInviteCode) {
		t.Fatalf("kode undangan ikut terhapus bersama group")
	}
	_ = other
}
