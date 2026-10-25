// Package testutil holds helpers for integration tests that need a real PostgreSQL.
package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/platformdb"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// EnvTestDatabaseURL names the variable that points integration tests at a disposable PostgreSQL.
// `make test-integration` starts one (docker compose --profile test) and sets it.
const EnvTestDatabaseURL = "TEST_DATABASE_URL"

// TestDatabaseURL returns TEST_DATABASE_URL, or skips the test when it is unset.
func TestDatabaseURL(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(EnvTestDatabaseURL)
	if dsn == "" {
		t.Skipf("%s is not set; skipping integration test", EnvTestDatabaseURL)
	}
	return dsn
}

// NewSchemaDB opens a connection pool bound to a fresh, empty schema and drops the schema when the test ends.
// Tests can run in parallel because each one gets its own schema.
func NewSchemaDB(t testing.TB) *sql.DB {
	t.Helper()
	base := TestDatabaseURL(t)

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("testutil: open %s: %v", EnvTestDatabaseURL, err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("testutil: connect %s: %v", EnvTestDatabaseURL, err)
	}

	schema := "t_" + strings.ReplaceAll(ids.New(), "-", "")
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("testutil: create schema: %v", err)
	}

	dsn, err := withSearchPath(base, schema)
	if err != nil {
		t.Fatalf("testutil: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testutil: open schema pool: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema))
	})
	return db
}

// NewPlatformDB returns a schema-isolated database with every platform migration applied.
func NewPlatformDB(t testing.TB) *sql.DB {
	t.Helper()
	db := NewSchemaDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := platformdb.Migrate(ctx, db); err != nil {
		t.Fatalf("testutil: migrate platform schema: %v", err)
	}
	return db
}

// withSearchPath adds search_path as a runtime parameter, so every pooled connection uses the test schema.
func withSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("%s must be a postgres:// URL", EnvTestDatabaseURL)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CredentialsKey returns a random TARGET_CREDENTIALS_KEY for a test.
func CredentialsKey(t testing.TB) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
