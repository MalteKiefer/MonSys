package main

// Audit-chain verifier dispatched by main.go's `--verify-audit-chain` flag.
// Kept in its own file so the verifier logic stays self-contained — main.go
// only owns the flag wiring; this file owns the side-effects (pool open,
// stdout summary, exit code mapping).

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MalteKiefer/MonSys/internal/server/store"
)

// RunAuditChainVerify connects to the database described by MON_DSN, runs
// Store.VerifyAuditChain, prints a one-line human-readable summary to stdout
// (or stderr on error), and returns a process exit code:
//
//	0 — chain intact (or empty audit_log).
//	1 — chain broken: prints the `at` of the first tampered/inconsistent row.
//	2 — operational error: MON_DSN missing, DB unreachable, query failed.
//
// The function is intentionally side-effect free aside from stdout/stderr
// writes: it opens its own pool and closes it before returning.
func RunAuditChainVerify() int {
	dsn := os.Getenv("MON_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "verify-audit-chain: MON_DSN required")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Open(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-audit-chain: db open: %v\n", err)
		return 2
	}
	defer st.Close()

	rows, brokenAt, err := st.VerifyAuditChain(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-audit-chain: %v\n", err)
		return 2
	}
	if !brokenAt.IsZero() {
		fmt.Fprintf(os.Stdout,
			"audit chain BROKEN: %d rows scanned, mismatch at %s\n",
			rows, brokenAt.UTC().Format(time.RFC3339Nano))
		return 1
	}
	fmt.Fprintf(os.Stdout, "audit chain OK: %d rows verified\n", rows)
	return 0
}
