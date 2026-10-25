package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"dbplatform/app/internal/admin"
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

	cipher, err := dbmanager.NewCredentialCipher(cfg.TargetCredentialsKey)
	if err != nil {
		log.Fatalf("%v", err)
	}

	sessionStore := authn.NewPostgresSessionStore(db, cfg.Session)
	groupRepo := groups.NewRepository(db)
	grantStore := rbac.NewPostgresGrantStore(db)
	authorizer := rbac.NewAuthorizer(grantStore)
	// Target pools: the viewer, the target manager and the migration engine share one registry.
	registry := dbmanager.NewConnectionRegistryWithLimits(cfg.Query)
	defer registry.Close()
	targets := dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, registry)
	adminService := admin.NewService(sessionStore, groupRepo, grantStore, targets)
	migrationEngine := migration.NewEngine(db, registry, cfg.Migration)
	migrations := migration.NewService(migration.NewRepository(db), migrationEngine, authorizer)

	admins, err := bootstrap.EnsureAdmin(ctx, sessionStore, cfg.BootstrapAdmins)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(admins) > 0 {
		log.Printf("Platform Admin awal dibuat: %s", strings.Join(admins, ", "))
	}

	if cfg.SeedDemoData {
		seeded, err := bootstrap.SeedDemo(ctx, sessionStore, groupRepo, cfg.DemoUsers, cfg.DemoGroups)
		if err != nil {
			log.Fatalf("gagal mengisi data demo: %v", err)
		}
		if len(seeded) > 0 {
			log.Printf("Data demo dibuat: %s", strings.Join(seeded, ", "))
		}
		registered, err := bootstrap.SeedDemoTarget(ctx, targets, grantStore, groupRepo, cfg.DemoTarget, cfg.DemoGroups)
		if err != nil {
			log.Fatalf("gagal mendaftarkan database demo: %v", err)
		}
		if registered {
			log.Printf("Database demo didaftarkan: %s", bootstrap.DemoTargetName)
		}
	}

	// Connect every registered target. Unreachable targets are marked ERROR, never fatal.
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), 60*time.Second)
	connected, failed, err := targets.LoadAll(loadCtx)
	cancelLoad()
	if err != nil {
		log.Printf("peringatan: gagal memuat database target: %v", err)
	} else {
		log.Printf("Database target: %d terhubung, %d gagal", connected, failed)
	}

	// The platform DB (users, sessions, groups) is never registered as a viewer target.
	srv := apphttp.NewServer(os.Stdout, apphttp.Deps{
		Sessions:   sessionStore,
		Groups:     groupRepo,
		Access:     authorizer,
		Admin:      adminService,
		Registry:   registry,
		Migrations: migrations,
	})

	log.Printf("Backend API berjalan pada %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
