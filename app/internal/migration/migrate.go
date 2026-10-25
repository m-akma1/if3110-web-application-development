package migration

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed 001_authn_schema.sql
var authnSchemaSQL string

// RunCoreMigrations mengeksekusi DDL skema PostgreSQL Internal aplikasi
func RunCoreMigrations(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("koneksi database nil")
	}
	_, err := db.ExecContext(ctx, authnSchemaSQL)
	if err != nil {
		return fmt.Errorf("gagal menjalankan migrasi skema authn: %w", err)
	}
	return nil
}
