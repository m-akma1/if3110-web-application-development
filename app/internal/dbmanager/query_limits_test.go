package dbmanager_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"dbplatform/app/internal/dbmanager"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestDefaultQueryLimits_FromEnv(t *testing.T) {
	t.Setenv("QUERY_TIMEOUT_MS", "1500")
	t.Setenv("QUERY_ROW_LIMIT", "42")

	limits := dbmanager.DefaultQueryLimits()
	if limits.Timeout != 1500*time.Millisecond {
		t.Fatalf("timeout seharusnya 1500ms, dapat: %s", limits.Timeout)
	}
	if limits.MaxRows != 42 {
		t.Fatalf("batas baris seharusnya 42, dapat: %d", limits.MaxRows)
	}
}

func TestDefaultQueryLimits_InvalidEnvFallsBack(t *testing.T) {
	t.Setenv("QUERY_TIMEOUT_MS", "bukan-angka")
	t.Setenv("QUERY_ROW_LIMIT", "-5")

	limits := dbmanager.DefaultQueryLimits()
	if limits.Timeout <= 0 || limits.MaxRows <= 0 {
		t.Fatalf("nilai env tidak valid seharusnya memakai fallback positif, dapat: %+v", limits)
	}
}

// openTestDB membuka PostgreSQL khusus pengujian dari TEST_DATABASE_URL, atau melewati test jika tidak disetel
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL tidak disetel, integration test dilewati")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("gagal membuka koneksi test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("gagal terhubung ke test DB: %v", err)
	}
	return db
}

func TestExecuteReadOnlyQuery_TruncatesAtRowLimit_Integration(t *testing.T) {
	db := openTestDB(t)

	res, err := dbmanager.ExecuteReadOnlyQuery(context.Background(), db,
		"SELECT g FROM generate_series(1, 100) AS g",
		dbmanager.QueryLimits{Timeout: 5 * time.Second, MaxRows: 10})
	if err != nil {
		t.Fatalf("kueri seharusnya berhasil: %v", err)
	}
	if len(res.Rows) != 10 || !res.Truncated {
		t.Fatalf("hasil seharusnya dipotong menjadi 10 baris dan ditandai Truncated, dapat: %d baris, truncated=%v", len(res.Rows), res.Truncated)
	}

	small, err := dbmanager.ExecuteReadOnlyQuery(context.Background(), db,
		"SELECT g FROM generate_series(1, 5) AS g",
		dbmanager.QueryLimits{Timeout: 5 * time.Second, MaxRows: 10})
	if err != nil {
		t.Fatalf("kueri seharusnya berhasil: %v", err)
	}
	if len(small.Rows) != 5 || small.Truncated {
		t.Fatalf("hasil kecil tidak boleh ditandai Truncated, dapat: %d baris, truncated=%v", len(small.Rows), small.Truncated)
	}
}

func TestExecuteReadOnlyQuery_StatementTimeout_Integration(t *testing.T) {
	db := openTestDB(t)

	start := time.Now()
	_, err := dbmanager.ExecuteReadOnlyQuery(context.Background(), db,
		"SELECT pg_sleep(3)",
		dbmanager.QueryLimits{Timeout: 200 * time.Millisecond, MaxRows: 10})
	if err == nil {
		t.Fatalf("kueri pg_sleep(3) seharusnya dibatalkan oleh statement_timeout")
	}
	if !strings.Contains(err.Error(), "statement timeout") && !strings.Contains(err.Error(), "57014") {
		t.Fatalf("error seharusnya berasal dari statement_timeout, dapat: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("kueri seharusnya berhenti mendekati batas 200ms, butuh: %s", time.Since(start))
	}
}

func TestExecuteReadOnlyQuery_BlocksWritesInTransaction_Integration(t *testing.T) {
	db := openTestDB(t)

	seq := "dbmanager_ro_test_seq"
	if _, err := db.Exec("CREATE SEQUENCE IF NOT EXISTS " + seq); err != nil {
		t.Fatalf("gagal menyiapkan sequence: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP SEQUENCE IF EXISTS " + seq) })

	// Lolos dari guard kata kunci (diawali SELECT), tetapi transaksi READ ONLY tetap menolak perubahan state
	_, err := dbmanager.ExecuteReadOnlyQuery(context.Background(), db,
		"SELECT nextval('"+seq+"')",
		dbmanager.QueryLimits{Timeout: 2 * time.Second, MaxRows: 10})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("nextval() seharusnya ditolak oleh transaksi read-only, dapat: %v", err)
	}
}
