package config

import (
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	_, err := load(envOf(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("tanpa DATABASE_URL seharusnya gagal, dapat: %v", err)
	}
}

func TestLoad_DefaultsAndSeed(t *testing.T) {
	cfg, err := load(envOf(map[string]string{
		"DATABASE_URL":           "postgres://x",
		"SEED_DEMO_DATA":         "true",
		"SEED_DEMO_PASSWORD":     "DemoPassword123!",
		"TARGET_CREDENTIALS_KEY": "a2V5",
	}))
	if err != nil {
		t.Fatalf("konfigurasi valid seharusnya lolos: %v", err)
	}
	if cfg.ListenAddr != ":8080" || !cfg.SeedDemoData {
		t.Fatalf("nilai default tidak sesuai: %+v", cfg)
	}
}

func TestLoad_SeedNeedsPassword(t *testing.T) {
	_, err := load(envOf(map[string]string{"DATABASE_URL": "postgres://x", "SEED_DEMO_DATA": "1"}))
	if err == nil || !strings.Contains(err.Error(), "SEED_DEMO_PASSWORD") {
		t.Fatalf("SEED_DEMO_DATA tanpa password seharusnya gagal, dapat: %v", err)
	}
}

func TestLoad_InvalidBool(t *testing.T) {
	_, err := load(envOf(map[string]string{"DATABASE_URL": "postgres://x", "SEED_DEMO_DATA": "maybe"}))
	if err == nil || !strings.Contains(err.Error(), "SEED_DEMO_DATA") {
		t.Fatalf("nilai boolean tidak valid seharusnya gagal, dapat: %v", err)
	}
}

func TestLoad_RequiresCredentialsKey(t *testing.T) {
	_, err := load(envOf(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err == nil || !strings.Contains(err.Error(), "TARGET_CREDENTIALS_KEY") {
		t.Fatalf("tanpa TARGET_CREDENTIALS_KEY seharusnya gagal, dapat: %v", err)
	}
}

func TestLoad_MigrationLimits(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "TARGET_CREDENTIALS_KEY": "a2V5"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	cfg, err := load(envOf(base))
	if err != nil || cfg.MigrationLockTimeout != 0 || cfg.MigrationStatementTimeout != 0 || cfg.MigrationQueueTimeout != 0 {
		t.Fatalf("tanpa variabel, batas migration seharusnya 0 (default engine): %+v, %v", cfg, err)
	}
	cfg, err = load(envOf(with(map[string]string{
		"MIGRATION_LOCK_TIMEOUT_MS":      "2500",
		"MIGRATION_STATEMENT_TIMEOUT_MS": "30000",
		"MIGRATION_QUEUE_TIMEOUT_MS":     "1000",
	})))
	if err != nil || cfg.MigrationLockTimeout != 2500*time.Millisecond || cfg.MigrationStatementTimeout != 30*time.Second || cfg.MigrationQueueTimeout != time.Second {
		t.Fatalf("batas migration tidak terbaca: %+v, %v", cfg, err)
	}
	for _, bad := range []string{"0", "-5", "abc", "1.5"} {
		_, err := load(envOf(with(map[string]string{"MIGRATION_STATEMENT_TIMEOUT_MS": bad})))
		if err == nil || !strings.Contains(err.Error(), "MIGRATION_STATEMENT_TIMEOUT_MS") {
			t.Errorf("nilai %q seharusnya ditolak, dapat: %v", bad, err)
		}
	}
}
