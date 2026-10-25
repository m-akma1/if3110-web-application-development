package config

import (
	"strings"
	"testing"
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
		"DATABASE_URL":       "postgres://x",
		"SEED_DEMO_DATA":     "true",
		"SEED_DEMO_PASSWORD": "DemoPassword123!",
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
