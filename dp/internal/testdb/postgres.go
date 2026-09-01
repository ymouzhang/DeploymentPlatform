// Package testdb provisions isolated PostgreSQL schemas for integration tests.
package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostgresURL creates an isolated schema and returns a connection URL scoped to it.
// The schema is removed after the test. Tests are skipped when DP_TEST_DATABASE_URL is unset.
func PostgresURL(t testing.TB) string {
	t.Helper()
	databaseURL := os.Getenv("DP_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DP_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL test database: %v", err)
	}
	schema := "dp_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close PostgreSQL setup connection: %v", err)
	}

	t.Cleanup(func() { dropSchema(t, databaseURL, identifier) })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL test URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func dropSchema(t testing.TB, databaseURL, identifier string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Errorf("connect to drop PostgreSQL test schema: %v", err)
		return
	}
	defer func() { _ = connection.Close(ctx) }()
	if _, err := connection.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
		t.Errorf("drop PostgreSQL test schema: %v", err)
	}
}
