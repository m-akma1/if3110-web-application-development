package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"dbplatform/app/internal/authn"
	apphttp "dbplatform/app/internal/http"
	"dbplatform/app/internal/migration"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	var (
		sessionStore *authn.SessionStore
		coreDB       *sql.DB
	)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		db, err := sql.Open("pgx", dbURL)
		if err != nil {
			log.Fatalf("gagal membuka koneksi PostgreSQL Internal: %v", err)
		}
		defer db.Close()
		coreDB = db

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			log.Fatalf("gagal terhubung ke PostgreSQL Internal: %v", err)
		}

		if err := migration.RunCoreMigrations(ctx, db); err != nil {
			log.Fatalf("gagal menjalankan migrasi database: %v", err)
		}

		sessionStore = authn.NewPostgresSessionStore(db, authn.DefaultSessionConfig())
		if err := sessionStore.SeedDefaultUsers(ctx); err != nil {
			log.Fatalf("gagal melakukan seed pengguna awal: %v", err)
		}
		log.Println("Terhubung ke PostgreSQL Internal & Session Lifecycle Engine aktif")
	} else {
		log.Println("DATABASE_URL tidak disetel, menggunakan in-memory SessionStore fallback")
		sessionStore = authn.NewSessionStore()
	}

	srv := apphttp.NewServerWithSessions(os.Stdout, sessionStore)
	if coreDB != nil {
		srv.RegisterDatabasePool("db-1", coreDB)
	}

	log.Println("Backend API berjalan pada :8080")
	if err := http.ListenAndServe(":8080", srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
