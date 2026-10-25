package dbmanager_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbplatform/app/internal/accesstest"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/testutil"
)

func TestTargetInput_ValidateAndDSN(t *testing.T) {
	ok := dbmanager.TargetInput{Name: "Staging", Host: "db.internal", DBName: "app", Username: "u", Password: "p@ss/w:rd?"}.Normalize()
	if err := ok.Validate(true); err != nil {
		t.Fatalf("input valid ditolak: %v", err)
	}
	if ok.Port != 5432 || ok.SSLMode != "disable" {
		t.Fatalf("default port/sslmode tidak diisi: %+v", ok)
	}
	dsn := dbmanager.DSN(dbmanager.Target{Host: ok.Host, Port: ok.Port, DBName: ok.DBName, Username: ok.Username, SSLMode: ok.SSLMode}, ok.Password)
	if strings.Contains(dsn, "p@ss/w:rd?") || !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("password harus di-escape di DSN: %s", dsn)
	}

	bad := []struct {
		field string
		in    dbmanager.TargetInput
	}{
		{"name", dbmanager.TargetInput{Name: "x", Host: "h", DBName: "d", Username: "u", Password: "p"}},
		{"host", dbmanager.TargetInput{Name: "ok", Host: "evil/host", DBName: "d", Username: "u", Password: "p"}},
		{"port", dbmanager.TargetInput{Name: "ok", Host: "h", Port: 70000, DBName: "d", Username: "u", Password: "p"}},
		{"db_name", dbmanager.TargetInput{Name: "ok", Host: "h", Username: "u", Password: "p"}},
		{"username", dbmanager.TargetInput{Name: "ok", Host: "h", DBName: "d", Password: "p"}},
		{"password", dbmanager.TargetInput{Name: "ok", Host: "h", DBName: "d", Username: "u"}},
		{"ssl_mode", dbmanager.TargetInput{Name: "ok", Host: "h", DBName: "d", Username: "u", Password: "p", SSLMode: "prefer"}},
	}
	for _, tc := range bad {
		var ve *dbmanager.ValidationError
		if err := tc.in.Normalize().Validate(true); !errors.As(err, &ve) || ve.Field != tc.field {
			t.Errorf("field %s seharusnya ditolak, dapat %v", tc.field, err)
		}
	}
	if err := (dbmanager.TargetInput{Name: "ok", Host: "h", DBName: "d", Username: "u"}).Normalize().Validate(false); err != nil {
		t.Errorf("saat edit, password kosong berarti tetap: %v", err)
	}
}

func TestTargetInputFromURL(t *testing.T) {
	in, err := dbmanager.TargetInputFromURL("Demo", "postgres://app:s%40cret@target-postgres:6543/demo?sslmode=require")
	if err != nil || in.Host != "target-postgres" || in.Port != 6543 || in.DBName != "demo" || in.Username != "app" || in.Password != "s@cret" || in.SSLMode != "require" {
		t.Fatalf("parsing URL salah: %+v, %v", in, err)
	}
	if _, err := dbmanager.TargetInputFromURL("Demo", "mysql://x"); err == nil {
		t.Fatalf("skema selain postgres seharusnya ditolak")
	}
}

type managerEnv struct {
	manager  *dbmanager.Manager
	registry *dbmanager.ConnectionRegistry
	repo     *dbmanager.TargetRepository
	good     dbmanager.TargetInput
}

func newManagerEnv(t *testing.T) (*managerEnv, func(key string) *dbmanager.Manager) {
	t.Helper()
	db := testutil.NewPlatformDB(t)
	good, err := dbmanager.TargetInputFromURL("Test Target", testutil.TestDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	key := testutil.CredentialsKey(t)
	repo := dbmanager.NewTargetRepository(db)
	build := func(k string) *dbmanager.Manager {
		cipher, err := dbmanager.NewCredentialCipher(k)
		if err != nil {
			t.Fatal(err)
		}
		reg := dbmanager.NewConnectionRegistry()
		t.Cleanup(reg.Close)
		return dbmanager.NewManager(repo, cipher, reg)
	}
	m := build(key)
	return &managerEnv{manager: m, registry: m.Registry(), repo: repo, good: good}, func(k string) *dbmanager.Manager {
		if k == "" {
			k = key
		}
		return build(k)
	}
}

func TestManager_Lifecycle_Integration(t *testing.T) {
	ctx := context.Background()
	env, restart := newManagerEnv(t)

	// Create with working credentials: CONNECTED, pool registered, password sealed.
	tgt, err := env.manager.Create(ctx, env.good)
	if err != nil {
		t.Fatalf("create gagal: %v", err)
	}
	if tgt.Status != dbmanager.StatusConnected || tgt.LastCheckedAt == nil {
		t.Fatalf("status seharusnya CONNECTED: %+v", tgt)
	}
	if _, ok := env.registry.GetPool(tgt.ID); !ok {
		t.Fatalf("pool seharusnya terdaftar setelah koneksi berhasil")
	}
	sealed, _ := env.repo.SealedPassword(ctx, tgt.ID)
	if env.good.Password != "" && strings.Contains(string(sealed), env.good.Password) {
		t.Fatalf("password tidak boleh tersimpan plaintext")
	}
	if _, err := env.manager.Create(ctx, env.good); !errors.Is(err, dbmanager.ErrTargetNameTaken) {
		t.Fatalf("nama duplikat seharusnya ditolak, dapat %v", err)
	}

	// Wrong password: still saved, ERROR with a reason that never contains the password.
	bad := env.good
	bad.Name = "Bad Target"
	bad.Password = "salah-total-123"
	badT, err := env.manager.Create(ctx, bad)
	if err != nil {
		t.Fatalf("target dengan koneksi gagal tetap disimpan, dapat error: %v", err)
	}
	if badT.Status != dbmanager.StatusError || badT.LastError == "" || strings.Contains(badT.LastError, "salah-total-123") {
		t.Fatalf("status ERROR dengan pesan tanpa password diharapkan: %+v", badT)
	}
	if _, ok := env.registry.GetPool(badT.ID); ok {
		t.Fatalf("target gagal tidak boleh punya pool")
	}

	// Edit with an empty password keeps the stored one; fixing the bad one connects it.
	keep := env.good
	keep.Password = ""
	keep.Name = "Test Target (renamed)"
	if upd, err := env.manager.Update(ctx, tgt.ID, keep); err != nil || upd.Status != dbmanager.StatusConnected {
		t.Fatalf("update tanpa password seharusnya tetap terhubung: %+v, %v", upd, err)
	}
	fixed := env.good
	fixed.Name = "Bad Target"
	if upd, err := env.manager.Update(ctx, badT.ID, fixed); err != nil || upd.Status != dbmanager.StatusConnected {
		t.Fatalf("memperbaiki password seharusnya terhubung: %+v, %v", upd, err)
	}

	// Restart: a new manager with a fresh registry reconnects everything.
	restarted := restart("")
	connected, failed, err := restarted.LoadAll(ctx)
	if err != nil || connected != 2 || failed != 0 {
		t.Fatalf("LoadAll seharusnya menghubungkan 2 target: %d/%d, %v", connected, failed, err)
	}

	// Wrong encryption key: targets become ERROR instead of crashing.
	wrongKey := restart(testutil.CredentialsKey(t))
	if _, failed, err := wrongKey.LoadAll(ctx); err != nil || failed != 2 {
		t.Fatalf("kunci salah seharusnya menandai target ERROR: failed=%d, %v", failed, err)
	}
	if got, _ := env.repo.Get(ctx, tgt.ID); got.Status != dbmanager.StatusError {
		t.Fatalf("status seharusnya ERROR setelah gagal membuka kredensial, dapat %s", got.Status)
	}
	if got, _ := env.manager.Test(ctx, tgt.ID); got.Status != dbmanager.StatusConnected {
		t.Fatalf("uji ulang dengan kunci benar seharusnya CONNECTED, dapat %s", got.Status)
	}

	// Delete closes the pool and removes the row.
	if err := env.manager.Delete(ctx, tgt.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.registry.GetPool(tgt.ID); ok {
		t.Fatalf("pool seharusnya ditutup setelah delete")
	}
	if _, err := env.manager.Get(ctx, tgt.ID); !errors.Is(err, dbmanager.ErrTargetNotFound) {
		t.Fatalf("target seharusnya terhapus, dapat %v", err)
	}
	if err := env.manager.Delete(ctx, tgt.ID); !errors.Is(err, dbmanager.ErrTargetNotFound) {
		t.Fatalf("hapus dua kali seharusnya ErrTargetNotFound, dapat %v", err)
	}
}

func TestManager_DeleteCascadesGrants_Integration(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewPlatformDB(t)
	cipher, _ := dbmanager.NewCredentialCipher(testutil.CredentialsKey(t))
	reg := dbmanager.NewConnectionRegistry()
	t.Cleanup(reg.Close)
	m := dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, reg)

	in := dbmanager.TargetInput{Name: "Unreachable", Host: "127.0.0.1", Port: 1, DBName: "x", Username: "x", Password: "x"}
	tgt, err := m.Create(ctx, in)
	if err != nil || tgt.Status != dbmanager.StatusError {
		t.Fatalf("host tidak terjangkau tetap disimpan dengan ERROR: %+v, %v", tgt, err)
	}
	accesstest.Grant(t, db, groups.PublicGroupID, tgt.ID, "VIEW_ONLY")
	if err := m.Delete(ctx, tgt.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_db_grants WHERE database_id = $1`, tgt.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("grant ke target yang dihapus seharusnya ikut terhapus, sisa %d", n)
	}
}
