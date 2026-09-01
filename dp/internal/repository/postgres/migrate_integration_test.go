package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"DP/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestMigrationRepeatAndChecksumProtection(t *testing.T) {
	databaseURL := testdb.PostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	first.Close()
	second, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	second.Close()

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to corrupt migration checksum: %v", err)
	}
	if _, err := connection.Exec(ctx, `UPDATE schema_migrations SET checksum = repeat('0', 64) WHERE version = 1`); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("corrupt migration checksum: %v", err)
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close checksum connection: %v", err)
	}

	if _, err := Open(ctx, databaseURL); err == nil || !strings.Contains(err.Error(), "migration 001 checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}
