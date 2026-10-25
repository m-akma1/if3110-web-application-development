package platformdb_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/platformdb"
	"dbplatform/app/internal/testutil"
)

func TestMigrations_AreOrderedAndNamed(t *testing.T) {
	list, err := platformdb.Migrations()
	if err != nil {
		t.Fatalf("membaca migrasi gagal: %v", err)
	}
	if len(list) == 0 || list[0].Version != 1 {
		t.Fatalf("migrasi pertama seharusnya versi 1, dapat: %+v", list)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Version <= list[i-1].Version {
			t.Fatalf("migrasi tidak terurut: %s setelah %s", list[i].Name, list[i-1].Name)
		}
	}
}

func TestMigrate_AppliesOnceAndIsIdempotent_Integration(t *testing.T) {
	db := testutil.NewSchemaDB(t)
	ctx := context.Background()

	applied, err := platformdb.Migrate(ctx, db)
	if err != nil {
		t.Fatalf("migrasi pertama gagal: %v", err)
	}
	all, _ := platformdb.Migrations()
	if len(applied) != len(all) {
		t.Fatalf("seharusnya %d migrasi diterapkan, dapat %v", len(all), applied)
	}

	again, err := platformdb.Migrate(ctx, db)
	if err != nil || len(again) != 0 {
		t.Fatalf("migrasi kedua seharusnya tidak menerapkan apa pun, dapat %v, err=%v", again, err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(all) {
		t.Fatalf("schema_migrations seharusnya berisi %d baris, dapat %d (%v)", len(all), n, err)
	}
}

func TestMigrate_RefusesLegacySchema_Integration(t *testing.T) {
	db := testutil.NewSchemaDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE users (id VARCHAR(64) PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := platformdb.Migrate(ctx, db); !errors.Is(err, platformdb.ErrLegacySchema) {
		t.Fatalf("skema lama tanpa schema_migrations seharusnya ditolak, dapat: %v", err)
	}
}

func TestPublicGroup_Invariants_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()

	userID := ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash) VALUES ($1, 'publik', 'h')`, userID); err != nil {
		t.Fatalf("insert user gagal: %v", err)
	}

	var member bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)`,
		platformdb.PublicGroupID, userID).Scan(&member); err != nil || !member {
		t.Fatalf("user baru seharusnya otomatis menjadi anggota Public (member=%v, err=%v)", member, err)
	}

	_, err := db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, platformdb.PublicGroupID, userID)
	if err == nil || !strings.Contains(err.Error(), "Public") {
		t.Fatalf("keanggotaan Public tidak boleh dihapus, dapat: %v", err)
	}

	_, err = db.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, platformdb.PublicGroupID)
	if err == nil || !strings.Contains(err.Error(), "cannot be deleted") {
		t.Fatalf("group sistem Public tidak boleh dihapus, dapat: %v", err)
	}

	// Menghapus user tetap boleh, keanggotaan Public ikut terhapus (ON DELETE CASCADE)
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("menghapus user seharusnya tetap boleh: %v", err)
	}
	var left int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_members WHERE user_id = $1`, userID).Scan(&left)
	if left != 0 {
		t.Fatalf("keanggotaan user yang dihapus seharusnya ikut terhapus, sisa: %d", left)
	}
}

func TestUsersTable_EnforcesLowercaseAndUniqueness_Integration(t *testing.T) {
	db := testutil.NewPlatformDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, 'Kapital', 'h')`, ids.New()); err == nil {
		t.Fatalf("username huruf besar seharusnya ditolak oleh CHECK constraint")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, email, password_hash) VALUES ($1, 'satu', NULL, 'h'), ($2, 'dua', NULL, 'h')`, ids.New(), ids.New()); err != nil {
		t.Fatalf("beberapa user tanpa email seharusnya boleh: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, role, password_hash) VALUES ($1, 'tiga', 'ROOT', 'h')`, ids.New()); err == nil {
		t.Fatalf("role di luar USER/PLATFORM_ADMIN seharusnya ditolak")
	}
}
