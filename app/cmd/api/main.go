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
	apphttp "dbplatform/app/internal/http"
	"dbplatform/app/internal/platformdb"

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
		seeded, err := bootstrap.SeedDemo(ctx, sessionStore, bootstrap.DemoConfig{Password: cfg.SeedDemoPassword})
		if err != nil {
			log.Fatalf("gagal mengisi data demo: %v", err)
		}
		defer db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			log.Fatalf("gagal terhubung ke PostgreSQL Internal: %v", err)

			if len(seeded) > 0 {
				log.Printf("Akun demo dibuat: %s", strings.Join(seeded, ", "))
			}
		}

		// The platform DB (users, sessions, groups) is never registered as a viewer target.
		srv := apphttp.NewServerWithSessions(os.Stdout, sessionStore)

		// Database platform (users, sessions) bukan target viewer.
		// Target untuk demo dipisah sebagai "db-1" untuk sementara
		if demoURL := os.Getenv("DEMO_TARGET_URL"); demoURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := srv.ConnectDatabase(ctx, "db-1", demoURL); err != nil {
				log.Printf("peringatan: gagal terhubung ke database target demo: %v", err)
			} else {
				log.Println("Terhubung ke database target demo sebagai db-1")
			}
			cancel()
		} else {
			log.Println("DEMO_TARGET_URL tidak disetel, tidak ada database target live yang terdaftar")
		}

		log.Printf("Backend API berjalan pada %s", cfg.ListenAddr)
		if err := http.ListenAndServe(cfg.ListenAddr, srv); err != nil {
			log.Fatalf("server error: %v", err)
		}
		log.Println("Terhubung ke PostgreSQL Internal & Session Lifecycle Engine aktif")
	} else {
		log.Println("DATABASE_URL tidak disetel, menggunakan in-memory SessionStore fallback")
		sessionStore = authn.NewSessionStore()
	}

	srv := apphttp.NewServerWithSessions(os.Stdout, sessionStore)

	// Database platform (users, sessions) bukan target viewer.
	// Target untuk demo dipisah sebagai "db-1" untuk sementara
	if demoURL := os.Getenv("DEMO_TARGET_URL"); demoURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := srv.ConnectDatabase(ctx, "db-1", demoURL); err != nil {
			log.Printf("peringatan: gagal terhubung ke database target demo: %v", err)
		} else {
			log.Println("Terhubung ke database target demo sebagai db-1")
		}
		cancel()
	} else {
		log.Println("DEMO_TARGET_URL tidak disetel, tidak ada database target live yang terdaftar")
	}

	log.Println("Backend API berjalan pada :8080")
	if err := http.ListenAndServe(":8080", srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
