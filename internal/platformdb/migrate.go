// Package platformdb owns the platform's own PostgreSQL schema (users, sessions, groups, ...).
// It is separate from internal/migration, which runs user-written migrations on target databases.
package platformdb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// advisoryLockKey serializes concurrent Migrate calls (for example two app replicas booting together).
const advisoryLockKey int64 = 0x0db0_9a7f_0001

// ErrLegacySchema means the database still has the pre-versioning schema (001_authn_schema.sql).
var ErrLegacySchema = errors.New("legacy platform schema found (tables exist but schema_migrations does not); " +
	"reset the platform database once, e.g. `docker compose down -v`")

// Migration is one embedded schema file, named NNNN_description.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns the embedded migrations sorted by version.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var list []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(e.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version <= 0 {
			return nil, fmt.Errorf("migration file %q must be named NNNN_description.sql", e.Name())
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migration version %d is used by both %q and %q", version, other, e.Name())
		}
		seen[version] = e.Name()
		body, err := migrationFiles.ReadFile(path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		list = append(list, Migration{Version: version, Name: e.Name(), SQL: string(body)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Version < list[j].Version })
	return list, nil
}

// Migrate applies every embedded migration that is not yet recorded in schema_migrations.
// Each file runs in its own transaction together with its schema_migrations row, so a failed
// file leaves no partial changes. It returns the names of the files applied by this call.
func Migrate(ctx context.Context, db *sql.DB) ([]string, error) {
	if db == nil {
		return nil, errors.New("platformdb: nil database")
	}
	migrations, err := Migrations()
	if err != nil {
		return nil, err
	}

	// Session-level advisory locks belong to one connection, so pin one for the whole run.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return nil, fmt.Errorf("platformdb: acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if err := checkLegacySchema(ctx, conn); err != nil {
		return nil, err
	}

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT        NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return nil, fmt.Errorf("platformdb: create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var done []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, conn, m); err != nil {
			return done, err
		}
		done = append(done, m.Name)
	}
	return done, nil
}

func applyOne(ctx context.Context, conn *sql.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("platformdb: apply %s: %w", m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name); err != nil {
		return fmt.Errorf("platformdb: record %s: %w", m.Name, err)
	}
	return tx.Commit()
}

// checkLegacySchema refuses to run on top of the old unversioned schema, which 001 would collide with.
func checkLegacySchema(ctx context.Context, conn *sql.Conn) error {
	var hasVersions, hasUsers bool
	err := conn.QueryRowContext(ctx, `
		SELECT to_regclass('schema_migrations') IS NOT NULL,
		       to_regclass('users') IS NOT NULL`).Scan(&hasVersions, &hasUsers)
	if err != nil {
		return err
	}
	if hasUsers && !hasVersions {
		return ErrLegacySchema
	}
	return nil
}

// PublicGroupID is the fixed id of the system "Public" group created by 001_identity.sql.
// Every user is a member; the triggers in that file keep it that way.
const PublicGroupID = "00000000-0000-0000-0000-000000000001"
