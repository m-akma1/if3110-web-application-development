package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/bootstrap"
	"dbplatform/app/internal/config"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	apphttp "dbplatform/app/internal/http"
	"dbplatform/app/internal/migration"
	"dbplatform/app/internal/platformdb"
	"dbplatform/app/internal/rbac"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("konfigurasi tidak valid:\n%v", err)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("gagal membuka koneksi PostgreSQL Internal: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("gagal terhubung ke PostgreSQL Internal: %v", err)
	}

	applied, err := platformdb.Migrate(ctx, db)
	if err != nil {
		log.Fatalf("gagal menjalankan migrasi skema platform: %v", err)
	}
	if len(applied) > 0 {
		log.Printf("Migrasi skema platform diterapkan: %s", strings.Join(applied, ", "))
	}

	sessionStore := authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
	groupRepo := groups.NewRepository(db)
	authorizer := rbac.NewAuthorizer(rbac.NewPostgresGrantStore(db))
	// Target pools: the viewer and the migration engine share one registry.
	registry := dbmanager.NewConnectionRegistry()
	migrationEngine := migration.NewEngine(db, registry, migration.Limits{
		LockTimeout:      cfg.MigrationLockTimeout,
		StatementTimeout: cfg.MigrationStatementTimeout,
		QueueTimeout:     cfg.MigrationQueueTimeout,
	})
	migrations := migration.NewService(migration.NewRepository(db), migrationEngine, authorizer)

	created, err := bootstrap.EnsureAdmin(ctx, sessionStore, bootstrap.AdminConfig{
		Username: cfg.BootstrapAdminUsername,
		Email:    cfg.BootstrapAdminEmail,
		Password: cfg.BootstrapAdminPassword,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}
	if created {
		log.Printf("Platform Admin awal dibuat: %s", cfg.BootstrapAdminUsername)
	}

	if cfg.SeedDemoData {
		seeded, err := bootstrap.SeedDemo(ctx, sessionStore, groupRepo, bootstrap.DemoConfig{Password: cfg.SeedDemoPassword})
		if err != nil {
			log.Fatalf("gagal mengisi data demo: %v", err)
		}
		if len(seeded) > 0 {
			log.Printf("Data demo dibuat: %s", strings.Join(seeded, ", "))
		}
	}

	// The platform DB (users, sessions, groups) is never registered as a viewer target.
	srv := apphttp.NewServer(os.Stdout, apphttp.Deps{
		Sessions:   sessionStore,
		Groups:     groupRepo,
		Access:     authorizer,
		Registry:   registry,
		Migrations: migrations,
	})

	log.Printf("Backend API berjalan pada %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
