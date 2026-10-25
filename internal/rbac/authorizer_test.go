package rbac_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/ids"
	"dbplatform/internal/rbac"
)

// stubStore is an in-memory GrantStore: userID -> list of grants (one per group membership).
type stubStore struct {
	grants map[string][]rbac.Grant
	err    error
}

func (s stubStore) LevelsForUser(_ context.Context, userID, dbID string) ([]rbac.AccessLevel, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []rbac.AccessLevel
	for _, g := range s.grants[userID] {
		if g.DatabaseID == dbID {
			out = append(out, g.Level)
		}
	}
	return out, nil
}

func (s stubStore) GrantsForUser(_ context.Context, userID string) ([]rbac.Grant, error) {
	return s.grants[userID], s.err
}

func TestIsAllowed_Matrix(t *testing.T) {
	actions := []rbac.Action{rbac.ActionViewSchema, rbac.ActionViewData, rbac.ActionViewMigration, rbac.ActionCreateMigration, rbac.ActionExecuteMigration}
	want := map[rbac.AccessLevel][]bool{
		rbac.AccessNone:     {false, false, false, false, false},
		rbac.AccessViewOnly: {true, true, true, false, false},
		rbac.AccessViewEdit: {true, true, true, true, true},
		"PLATFORM_ADMIN":    {false, false, false, false, false}, // admin is not an access level
	}
	for level, expected := range want {
		for i, a := range actions {
			if got := rbac.IsAllowed(level, a); got != expected[i] {
				t.Errorf("IsAllowed(%s, %s) = %v, ingin %v", level, a, got, expected[i])
			}
		}
	}
	if rbac.IsAllowed(rbac.AccessViewEdit, "database:drop_everything") {
		t.Errorf("aksi yang tidak dikenal tidak boleh diizinkan")
	}
}

func TestEffective_MostPermissiveWins(t *testing.T) {
	cases := []struct {
		levels []rbac.AccessLevel
		want   rbac.AccessLevel
	}{
		{nil, rbac.AccessNone},
		{[]rbac.AccessLevel{rbac.AccessViewOnly}, rbac.AccessViewOnly},
		{[]rbac.AccessLevel{rbac.AccessViewOnly, rbac.AccessViewEdit}, rbac.AccessViewEdit},
		{[]rbac.AccessLevel{rbac.AccessViewEdit, rbac.AccessViewOnly}, rbac.AccessViewEdit},
		{[]rbac.AccessLevel{rbac.AccessNone, rbac.AccessViewOnly, rbac.AccessNone}, rbac.AccessViewOnly},
		{[]rbac.AccessLevel{"GARBAGE"}, rbac.AccessNone},
	}
	for _, tc := range cases {
		if got := rbac.Effective(tc.levels...); got != tc.want {
			t.Errorf("Effective(%v) = %s, ingin %s", tc.levels, got, tc.want)
		}
	}
}

func TestAuthorize(t *testing.T) {
	ctx := context.Background()
	user, other := ids.New(), ids.New()
	dbA, dbB, dbC := ids.New(), ids.New(), ids.New()
	store := stubStore{grants: map[string][]rbac.Grant{
		// user is in Public (VIEW_ONLY on A) and Engineers (VIEW_EDIT on A, VIEW_ONLY on B)
		user: {
			{DatabaseID: dbA, DatabaseName: "alpha", Level: rbac.AccessViewOnly},
			{DatabaseID: dbA, DatabaseName: "alpha", Level: rbac.AccessViewEdit},
			{DatabaseID: dbB, DatabaseName: "beta", Level: rbac.AccessViewOnly},
		},
		other: {{DatabaseID: dbC, DatabaseName: "gamma", Level: rbac.AccessViewEdit}},
	}}
	az := rbac.NewAuthorizer(store)

	cases := []struct {
		name   string
		db     string
		action rbac.Action
		level  rbac.AccessLevel
		denied bool
	}{
		{"view+edit lewat salah satu group cukup untuk eksekusi", dbA, rbac.ActionExecuteMigration, rbac.AccessViewEdit, false},
		{"view only boleh lihat riwayat migrasi", dbB, rbac.ActionViewMigration, rbac.AccessViewOnly, false},
		{"view only tidak boleh membuat migrasi", dbB, rbac.ActionCreateMigration, rbac.AccessViewOnly, true},
		{"tanpa grant ditolak", dbC, rbac.ActionViewData, rbac.AccessNone, true},
		{"database tidak dikenal ditolak", ids.New(), rbac.ActionViewSchema, rbac.AccessNone, true},
		{"id bukan UUID ditolak", "db-1", rbac.ActionViewSchema, rbac.AccessNone, true},
		{"id kosong ditolak", "", rbac.ActionViewSchema, rbac.AccessNone, true},
	}
	for _, tc := range cases {
		level, err := az.Authorize(ctx, user, tc.db, tc.action)
		if level != tc.level {
			t.Errorf("%s: level %s, ingin %s", tc.name, level, tc.level)
		}
		if tc.denied != errors.Is(err, rbac.ErrForbidden) {
			t.Errorf("%s: err=%v, ingin ditolak=%v", tc.name, err, tc.denied)
		}
	}
}

func TestAuthorize_StorageErrorDenies(t *testing.T) {
	boom := errors.New("db down")
	az := rbac.NewAuthorizer(stubStore{err: boom})
	if _, err := az.Authorize(context.Background(), ids.New(), ids.New(), rbac.ActionViewData); !errors.Is(err, boom) {
		t.Fatalf("error penyimpanan harus diteruskan (dan diperlakukan sebagai penolakan), dapat: %v", err)
	}
}

func TestAccessibleDatabases_AggregatesAndSorts(t *testing.T) {
	user := ids.New()
	dbA, dbB := ids.New(), ids.New()
	az := rbac.NewAuthorizer(stubStore{grants: map[string][]rbac.Grant{user: {
		{DatabaseID: dbB, DatabaseName: "zeta", Level: rbac.AccessViewOnly},
		{DatabaseID: dbA, DatabaseName: "alpha", Level: rbac.AccessViewOnly},
		{DatabaseID: dbB, DatabaseName: "zeta", Level: rbac.AccessViewEdit},
	}}})

	list, err := az.AccessibleDatabases(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].DatabaseName != "alpha" || list[1].DatabaseName != "zeta" {
		t.Fatalf("seharusnya [alpha zeta] terurut, dapat %+v", list)
	}
	if list[1].Level != rbac.AccessViewEdit || list[0].Level != rbac.AccessViewOnly {
		t.Fatalf("level efektif salah: %+v", list)
	}

	none, _ := az.AccessibleDatabases(context.Background(), ids.New())
	if len(none) != 0 {
		t.Fatalf("user tanpa grant seharusnya tidak melihat database, dapat %+v", none)
	}
}
