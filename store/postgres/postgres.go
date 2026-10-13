// Package postgres implements the aiassist stores on PostgreSQL (pgx v5)
// and ships the schema as embedded migrations with a tiny runner, so a
// project installs the template with one Migrate call and no extra tooling.
package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations exposes the embedded schema (e.g. to copy into a project's own
// golang-migrate directory instead of calling Migrate).
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

// DB is the subset of *pgxpool.Pool (or *pgx.Conn) the stores use.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Migrate applies pending embedded migrations, each in its own
// transaction, tracking versions in ai_schema_migrations. An advisory lock
// keeps concurrent replicas from racing.
func Migrate(ctx context.Context, db DB) error {
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS ai_schema_migrations (
		version INT PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return fmt.Errorf("create ai_schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	type mig struct {
		version int
		name    string
	}
	var migs []mig
	for _, e := range entries {
		name := e.Name()
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", name)
		}
		migs = append(migs, mig{v, name})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for _, m := range migs {
		if err := applyOne(ctx, db, m.version, m.name); err != nil {
			return err
		}
	}
	return nil
}

// migrationLockKey is an arbitrary constant for pg_advisory_xact_lock.
const migrationLockKey = 7426310921

func applyOne(ctx context.Context, db DB, version int, name string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx)
	}
	sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ai_schema_migrations (version, name) VALUES ($1, $2)`, version, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
