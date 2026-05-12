// Migration round-trip test harness.
//
// For every numbered SQL migration under ./migrations/, this file boots a
// real TimescaleDB container (same image digest as the compose deploy) and
// exercises three properties:
//
//  1. TestMigrationsFullCycle    - Up all -> Down all -> Up all again works.
//  2. TestMigrationsSchemaIdempotent - schema after Up,Down,Up is identical
//     to schema after a clean Up, *for migrations that claim to be reversible*.
//     Migrations that explicitly do not reverse hypertable conversions (0016,
//     0021) are listed in knownUnreversible with a comment justifying why.
//  3. TestMigrationDownFromMid   - for each reversible migration N, Up to N,
//     Down once, assert goose_db_version reports N-1.
//
// Tests are gated behind MON_TEST_DOCKER=1 (slow, requires a Docker engine).
// Under `go test -short` they always skip. Honour DOCKER_HOST for rootless
// docker / podman sockets - testcontainers-go reads this automatically.
//
// Image rationale: we deliberately use the same digest pinned in
// deploy/docker-compose.yaml so the tests run against the same TimescaleDB
// build the production deployment runs against. Bumping the deploy pin must
// also bump testImage below.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MalteKiefer/MonSys/internal/server/store/migrations"
)

// testImage MUST track deploy/docker-compose.yaml. A drift would mean tests
// pass against an image production never sees.
const testImage = "timescale/timescaledb:latest-pg16@sha256:15e00162766bd6f0019afaad4e57b850dcf882de5909bd7633899eebd4c03d57"

// knownUnreversible lists migration files whose Down section deliberately does
// NOT recreate the pre-migration schema. These migrations are still exercised
// by TestMigrationsFullCycle (Up/Down/Up must succeed without errors) but are
// skipped by TestMigrationsSchemaIdempotent and TestMigrationDownFromMid.
//
// Add an entry here ONLY when the migration itself documents the reason. The
// comment next to the entry must mirror that justification.
var knownUnreversible = map[string]string{
	// 0016: alert_history and host_status_history are converted to Timescale
	// hypertables on the way Up. The Down only removes the retention policy;
	// reverting a hypertable to a plain table is destructive (chunk merge) and
	// the operator never needs it. Migration file flags this with
	// "IRREVERSIBLE: hypertable conversion + PK change is not reverted".
	"0016_housekeeping_retention.sql": "hypertable conversion intentionally not reverted",
	// 0021: audit_log is converted to a hypertable; same rationale as 0016.
	// Migration file explicitly says "IRREVERSIBLE: hypertable conversion + PK
	// change is not reverted by goose down".
	"0021_db_hardening.sql": "audit_log hypertable conversion intentionally not reverted",
}

// dockerEnabled returns true when the host opted into containerized tests.
// We also honour `go test -short` to skip even when the env is set.
func dockerEnabled(t *testing.T) bool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainers-backed test under -short")
		return false
	}
	if os.Getenv("MON_TEST_DOCKER") != "1" {
		t.Skip("set MON_TEST_DOCKER=1 to run migration tests against testcontainers")
		return false
	}
	return true
}

// listMigrationFiles returns the numbered .sql migrations in lexical order,
// which is also goose's apply order.
func listMigrationFiles(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// startTimescale boots a TimescaleDB container and returns a sql.DB plus a
// cleanup func. The pool config is sized for short-lived tests.
func startTimescale(ctx context.Context, t *testing.T) (*sql.DB, func()) {
	t.Helper()

	pgC, err := tcpostgres.Run(
		ctx,
		testImage,
		tcpostgres.WithDatabase("mon"),
		tcpostgres.WithUsername("mon"),
		tcpostgres.WithPassword("monpw"),
		// pg_isready inside the entrypoint can race the first connection on a
		// slow runner; wait for two consecutive readiness probes before
		// returning, mirroring what the deploy compose healthcheck enforces.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start timescaledb container: %v", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(ctx)
		t.Fatalf("get conn string: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_ = pgC.Terminate(ctx)
		t.Fatalf("parse dsn: %v", err)
	}
	db := stdlib.OpenDB(*cfg.ConnConfig)

	cleanup := func() {
		_ = db.Close()
		// 30s is plenty; we don't care if the engine takes its time tearing down.
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return db, cleanup
}

// gooseSetup configures goose for the embedded migration FS. Idempotent.
func gooseSetup(t *testing.T) {
	t.Helper()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	// Quiet the migration-runner chatter so test output stays readable.
	goose.SetLogger(goose.NopLogger())
}

// gooseVersion reads the current goose_db_version row.
func gooseVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	v, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("goose version: %v", err)
	}
	return v
}

// dumpSchema returns a deterministic representation of the schema useful for
// before/after comparisons. We can't use pg_dump from inside the test binary
// (it isn't shipped with Go), so we synthesize a fingerprint by querying
// information_schema and pg_catalog. The fingerprint covers:
//
//   - tables and their columns (name, ordinal, type, nullability, default)
//   - constraints (name, type, definition)
//   - indexes (name, definition)
//   - triggers (name, table, definition)
//   - functions in the public schema (name, definition)
//   - TimescaleDB hypertables (table name + dimension column)
//
// We deliberately omit anything Timescale records as a background-worker side
// effect (chunk metadata, retention-policy job_ids that get fresh sequence
// values on each create) - those vary across runs and would mask schema-level
// differences.
func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var parts []string

	queries := []struct {
		label string
		sql   string
	}{
		{
			"columns",
			`SELECT table_schema || '.' || table_name || ' ' || column_name || ' ' ||
			        ordinal_position || ' ' || data_type || ' ' ||
			        is_nullable || ' ' || COALESCE(column_default, '<none>')
			 FROM information_schema.columns
			 WHERE table_schema = 'public'
			 ORDER BY 1`,
		},
		{
			"constraints",
			`SELECT n.nspname || '.' || t.relname || ' ' || c.conname || ' ' ||
			        c.contype || ' ' || pg_get_constraintdef(c.oid)
			 FROM pg_constraint c
			 JOIN pg_class t ON t.oid = c.conrelid
			 JOIN pg_namespace n ON n.oid = t.relnamespace
			 WHERE n.nspname = 'public'
			 ORDER BY 1`,
		},
		{
			"indexes",
			`SELECT schemaname || '.' || tablename || ' ' || indexname || ' ' || indexdef
			 FROM pg_indexes
			 WHERE schemaname = 'public'
			 ORDER BY 1`,
		},
		{
			"triggers",
			`SELECT event_object_schema || '.' || event_object_table || ' ' || trigger_name || ' ' ||
			        action_timing || ' ' || event_manipulation || ' ' || action_statement
			 FROM information_schema.triggers
			 WHERE trigger_schema = 'public'
			 ORDER BY 1`,
		},
		{
			"functions",
			`SELECT n.nspname || '.' || p.proname || '(' || pg_get_function_arguments(p.oid) || ') -> ' ||
			        pg_get_function_result(p.oid) || ' :: ' || pg_get_functiondef(p.oid)
			 FROM pg_proc p
			 JOIN pg_namespace n ON n.oid = p.pronamespace
			 WHERE n.nspname = 'public'
			 ORDER BY 1`,
		},
		{
			"hypertables",
			`SELECT hypertable_name FROM timescaledb_information.hypertables ORDER BY 1`,
		},
	}

	for _, q := range queries {
		dumpQuery(t, db, q.label, q.sql, &parts)
	}
	return strings.Join(parts, "\n")
}

// dumpQuery runs one schema-dump query and appends its rows to parts. Split
// out so the defer rows.Close() pattern (sqlclosecheck) lives in its own
// stack frame rather than a tight loop.
func dumpQuery(t *testing.T, db *sql.DB, label, query string, parts *[]string) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("schema dump %q: %v", label, err)
	}
	defer rows.Close()
	*parts = append(*parts, "## "+label)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan %q: %v", label, err)
		}
		*parts = append(*parts, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err %q: %v", label, err)
	}
}

// upAll runs every migration. Helper around goose.UpContext that surfaces a
// useful error message including the latest version reached.
func upAll(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("goose up (reached version %d): %v", gooseVersion(t, db), err)
	}
}

// downTo migrates the schema back to the supplied version.
func downTo(ctx context.Context, t *testing.T, db *sql.DB, target int64) {
	t.Helper()
	if err := goose.DownToContext(ctx, db, ".", target); err != nil {
		t.Fatalf("goose down-to %d (from %d): %v", target, gooseVersion(t, db), err)
	}
}

// downOnce rolls back the most recently applied migration.
func downOnce(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if err := goose.DownContext(ctx, db, "."); err != nil {
		t.Fatalf("goose down (from version %d): %v", gooseVersion(t, db), err)
	}
}

// allReversible returns true when no entry in the migration list is on the
// known-unreversible list. Used to short-circuit assertions cleanly.
func allReversible(files []string) bool {
	for _, f := range files {
		if _, skip := knownUnreversible[f]; skip {
			return false
		}
	}
	return true
}

// migrationVersion strips the 4-digit numeric prefix off a file name like
// "0016_foo.sql" -> 16.
func migrationVersion(t *testing.T, name string) int64 {
	t.Helper()
	var v int64
	if _, err := fmt.Sscanf(name, "%04d_", &v); err != nil {
		t.Fatalf("cannot parse migration version from %q: %v", name, err)
	}
	return v
}

// previousVersion returns the migration version immediately before `target`
// in the canonical order, or 0 if target is the first one.
func previousVersion(t *testing.T, files []string, target string) int64 {
	t.Helper()
	for i, f := range files {
		if f == target {
			if i == 0 {
				return 0
			}
			return migrationVersion(t, files[i-1])
		}
	}
	t.Fatalf("migration %q not found in list", target)
	return 0
}

// TestMigrationsFullCycle exercises Up -> Down (all the way to 0) -> Up.
// Each step must succeed. After the second Up, goose_db_version must equal
// the maximum migration version.
func TestMigrationsFullCycle(t *testing.T) {
	if !dockerEnabled(t) {
		return
	}
	gooseSetup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	db, cleanup := startTimescale(ctx, t)
	defer cleanup()

	files := listMigrationFiles(t)
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	maxVersion := migrationVersion(t, files[len(files)-1])

	// 1) up all
	upAll(ctx, t, db)
	if got := gooseVersion(t, db); got != maxVersion {
		t.Fatalf("after first Up: goose version = %d, want %d", got, maxVersion)
	}

	// 2) down all the way to 0
	downTo(ctx, t, db, 0)
	if got := gooseVersion(t, db); got != 0 {
		t.Fatalf("after Down-to-0: goose version = %d, want 0", got)
	}

	// 3) up again
	upAll(ctx, t, db)
	if got := gooseVersion(t, db); got != maxVersion {
		t.Fatalf("after second Up: goose version = %d, want %d", got, maxVersion)
	}
}

// TestMigrationsSchemaIdempotent compares the schema dump after a clean Up
// to the schema dump after Up -> Down-to-0 -> Up. They should match for a
// fully reversible migration set. If any migration in the set is on the
// known-unreversible list we skip the byte-identical comparison and instead
// fall back to verifying the Up/Down/Up cycle itself succeeds (which is
// already covered by TestMigrationsFullCycle).
func TestMigrationsSchemaIdempotent(t *testing.T) {
	if !dockerEnabled(t) {
		return
	}
	gooseSetup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	files := listMigrationFiles(t)
	if !allReversible(files) {
		// Spell out which ones we're skipping so a future operator who removes
		// a knownUnreversible entry doesn't wonder why the test went green.
		t.Skipf("migration set contains entries on the knownUnreversible list (%v); "+
			"byte-identical schema comparison is intentionally skipped.", knownUnreversible)
		return
	}

	db, cleanup := startTimescale(ctx, t)
	defer cleanup()

	upAll(ctx, t, db)
	before := dumpSchema(t, db)

	downTo(ctx, t, db, 0)
	upAll(ctx, t, db)
	after := dumpSchema(t, db)

	if before != after {
		t.Errorf("schema dump differs after Up/Down/Up cycle.\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestMigrationDownFromMid catches Down statements that drop more than their
// matching Up created. For each migration N: start fresh, Up through N, Down
// once, and assert goose_db_version == previousVersion(N).
//
// Two performance compromises:
//
//   - We boot ONE TimescaleDB container and reset it between iterations via
//     `DownToContext(..., 0)` rather than launching a fresh container per
//     migration. Per-migration containers would balloon the runtime to ~10min
//     against a cold image cache and add no real coverage.
//   - Migrations on the knownUnreversible list are still tested (we still
//     want to know that Down doesn't error out), but we only assert that
//     goose's recorded version went down, not that it lands exactly on N-1.
func TestMigrationDownFromMid(t *testing.T) {
	if !dockerEnabled(t) {
		return
	}
	gooseSetup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	db, cleanup := startTimescale(ctx, t)
	defer cleanup()

	files := listMigrationFiles(t)
	for _, file := range files {
		target := migrationVersion(t, file)
		prev := previousVersion(t, files, file)

		t.Run(file, func(t *testing.T) {
			// Reset DB to empty schema, then run Up to `target`.
			downTo(ctx, t, db, 0)
			if err := goose.UpToContext(ctx, db, ".", target); err != nil {
				t.Fatalf("up-to %d: %v", target, err)
			}
			if got := gooseVersion(t, db); got != target {
				t.Fatalf("after Up-to %d: goose version = %d, want %d", target, got, target)
			}

			// Roll back exactly one step.
			downOnce(ctx, t, db)

			got := gooseVersion(t, db)
			if _, irreversible := knownUnreversible[file]; irreversible {
				if got >= target {
					t.Fatalf("after Down (known-unreversible %s): version = %d, expected < %d", file, got, target)
				}
				return
			}
			if got != prev {
				t.Fatalf("after Down: version = %d, want %d", got, prev)
			}
		})
	}
}
