package main

// Backup / restore preflight commands. Dispatched by main.go's
// `--backup-preflight` and `--restore-preflight` flags.
//
// Why preflight only, not a real backup?
//
//   pg_dump ships as a separate binary; it is *not* available inside the
//   distroless `mon-server` image. Re-implementing pg_dump in Go (table
//   COPY + dependency-ordered DDL) is fragile and a maintenance hole for
//   no real win — the existing operator path (`docker compose exec -T
//   timescaledb pg_dump …`) already works and is well-trodden.
//
//   What operators actually need from the binary is *confidence* before
//   they pull or restore a dump:
//
//     - "Which schema version am I about to capture?"
//     - "Is the audit chain intact at this exact moment?"
//     - "How many rows in the heavy tables — does the dump size look
//        plausible afterwards?"
//     - "What's the exact command to run?"
//
//   `--backup-preflight` answers those questions in one shot, then
//   prints the verbatim `docker compose exec … pg_dump …` invocation.
//   `--restore-preflight` does the symmetric check against a candidate
//   target DSN: is it empty, or does its schema version match what this
//   binary expects.
//
// Constraints:
//   - No new dependencies (pgx/v5 already in go.mod via the store
//     package).
//   - Reuse store.Open + store.VerifyAuditChain so we don't duplicate
//     the hash-chain SQL.
//   - Stdout is the operator's terminal: human-readable text, not JSON.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/MalteKiefer/MonSys/internal/server/store"
)

// preflightTables is the row-count summary list. Chosen to give the
// operator a feel for the dump size and to flag obvious "wrong DB"
// mistakes (e.g. preflight on a fresh DB shows 0 hosts, 0 users — abort).
var preflightTables = []struct{ name, sql string }{
	{"hosts", "SELECT count(*) FROM hosts"},
	{"users", "SELECT count(*) FROM users"},
	{"audit_log", "SELECT count(*) FROM audit_log"},
	{"alert_history", "SELECT count(*) FROM alert_history"},
	{"notification_rules", "SELECT count(*) FROM notification_rules"},
}

// runBackupPreflight connects to the DSN, reports schema version + key
// row counts + audit-chain status, and prints the exact pg_dump command
// the operator should run from the host. Output goes to `out` so tests
// can capture it; production callers pass os.Stdout.
//
// The function intentionally does *not* run pg_dump itself — see the
// file header for the rationale. It is a pre-flight checker plus a
// command-cheatsheet emitter.
func runBackupPreflight(ctx context.Context, dsn string, out io.Writer) error {
	if dsn == "" {
		return errors.New("MON_DSN required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer st.Close()

	fmt.Fprintln(out, "MonSys backup preflight")
	fmt.Fprintf(out, "  timestamp:      %s\n", time.Now().UTC().Format(time.RFC3339))

	version, err := readSchemaVersion(ctx, st)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	fmt.Fprintf(out, "  schema version: %d\n", version)

	// Re-use the existing audit-chain verifier so we don't duplicate
	// the hash-chain logic. A broken chain is reported but does not
	// abort the preflight — the dump itself remains useful as forensic
	// evidence even if the chain is suspect. Older deployments at
	// schema < 26 don't have the `hash` column yet; the verifier
	// surfaces a SQLSTATE 42703 which we report as UNKNOWN.
	chainRows, brokenAt, err := st.VerifyAuditChain(ctx)
	switch {
	case err != nil:
		fmt.Fprintf(out, "  audit chain:    UNKNOWN (%v)\n", err)
	case !brokenAt.IsZero():
		fmt.Fprintf(out, "  audit chain:    BROKEN at %s (%d rows scanned)\n",
			brokenAt.UTC().Format(time.RFC3339Nano), chainRows)
	default:
		fmt.Fprintf(out, "  audit chain:    OK (%d rows verified)\n", chainRows)
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Row counts:")
	for _, t := range preflightTables {
		var n int64
		if err := st.Pool.QueryRow(ctx, t.sql).Scan(&n); err != nil {
			fmt.Fprintf(out, "  %-22s ERROR: %v\n", t.name+":", err)
			continue
		}
		fmt.Fprintf(out, "  %-22s %d\n", t.name+":", n)
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "To capture the dump, run on the host:")
	fmt.Fprintln(out, "  cd /srv/mon/deploy")
	fmt.Fprintln(out, "  sudo docker compose exec -T timescaledb \\")
	fmt.Fprintln(out, "    pg_dump -U mon -d mon --clean --if-exists --no-owner --no-privileges \\")
	// dateFmt is concatenated at runtime so `go vet`'s Printf check
	// doesn't mistake the %Y/%m/%S inside the shell date format for
	// format directives.
	dateFmt := "%Y%m%dT%H%M%SZ"
	fmt.Fprintln(out, "    | gzip -c > backup-$(date -u +"+dateFmt+").sql.gz")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Verify the dump is non-empty before deleting the previous backup:")
	fmt.Fprintln(out, "  gunzip -c backup-*.sql.gz | wc -l")
	return nil
}

// runRestorePreflight connects to a *candidate target* DSN and reports
// whether it is safe to restore into: either the DB is empty (no goose
// table), or its schema is present but no user/audit rows yet. A
// populated target is flagged with a verbatim DROP SCHEMA recipe.
//
// The function is intentionally read-only: it never executes psql or
// pg_restore. It exists to surface the "you're about to overwrite a
// populated DB" footgun before the operator pipes the dump in.
func runRestorePreflight(ctx context.Context, dsn string, out io.Writer) error {
	if dsn == "" {
		return errors.New("MON_DSN required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer st.Close()

	fmt.Fprintln(out, "MonSys restore preflight")
	fmt.Fprintf(out, "  timestamp:      %s\n", time.Now().UTC().Format(time.RFC3339))

	// Detect "empty target" by checking for the goose_db_version table.
	// A pristine DB created by `docker compose up timescaledb` for the
	// first time has no schema; we recognise that and report it as the
	// safe case.
	var hasGoose bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM information_schema.tables
		    WHERE table_schema = 'public' AND table_name = 'goose_db_version'
		 )`).Scan(&hasGoose); err != nil {
		return fmt.Errorf("probe goose_db_version: %w", err)
	}

	if !hasGoose {
		fmt.Fprintln(out, "  target state:   EMPTY (no goose_db_version table)")
		fmt.Fprintln(out, "  result:         SAFE — restore directly into this DB")
	} else {
		version, err := readSchemaVersion(ctx, st)
		if err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		// users+audit_log are the most catastrophic to clobber by
		// accident; surface their combined count as the "is this DB
		// live?" signal. We ignore errors here because tables may not
		// exist on partial migrations — a missing table just means
		// nonEmpty stays 0 and we treat the DB as restorable.
		var nonEmpty int64
		_ = st.Pool.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM users) + (SELECT count(*) FROM audit_log)`,
		).Scan(&nonEmpty)
		fmt.Fprintf(out, "  target state:   POPULATED (schema version %d)\n", version)
		fmt.Fprintf(out, "  users+audit:    %d rows\n", nonEmpty)
		if nonEmpty > 0 {
			fmt.Fprintln(out, "  result:         REFUSE — target has live data; restore would clobber it")
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "If you intend to overwrite anyway, first drop the schema on the target:")
			fmt.Fprintln(out, "  docker compose exec -T timescaledb \\")
			fmt.Fprintln(out, "    psql -U mon -d mon -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;'")
			return nil
		}
		fmt.Fprintln(out, "  result:         SAFE — schema present but no live rows")
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "To perform the restore, run on the host:")
	fmt.Fprintln(out, "  cd /srv/mon/deploy")
	fmt.Fprintln(out, "  gunzip -c backup-XXX.sql.gz | docker compose exec -T timescaledb \\")
	fmt.Fprintln(out, "    psql -U mon -d mon -v ON_ERROR_STOP=1")
	return nil
}

// readSchemaVersion returns the highest applied migration. The
// goose_db_version table has one row per Up/Down with monotonically
// increasing version_id; MAX(is_applied = true) gives the current state
// regardless of whether the operator has rolled back at some point.
func readSchemaVersion(ctx context.Context, st *store.Store) (int64, error) {
	var v *int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = true`,
	).Scan(&v); err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}
