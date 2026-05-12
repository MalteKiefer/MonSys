package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/MalteKiefer/MonSys/internal/server/store/migrations"
	"github.com/MalteKiefer/MonSys/internal/server/webauthn"
)

type Store struct {
	Pool *pgxpool.Pool

	// Webauthn is the configured WebAuthn relying-party service, set by
	// main.go at startup. Nil when WebAuthn isn't configured (env vars
	// missing); the passkey store methods detect that and return a clear
	// error so the API layer can surface HTTP 503.
	Webauthn *webauthn.Service
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	dsn, err := withPasswordFile(dsn)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool parse: %w", err)
	}
	// Attach the OTel tracer to the pool so every Query/Exec/QueryRow gets
	// a child span. WithIncludeQueryParameters is intentionally OFF so we
	// never ship credentials or PII to the collector via bind parameters.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithTrimSQLInSpanName(),
	)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}

// MigrateUp applies embedded SQL migrations using goose.
func (s *Store) MigrateUp(ctx context.Context) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(migrations.FS)

	cfg := s.Pool.Config().ConnConfig
	db := stdlib.OpenDB(*cfg)
	defer db.Close()

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// withPasswordFile expands MON_DSN_PASSWORD_FILE if set, embedding the password
// into the DSN. Useful for Docker secrets.
func withPasswordFile(dsn string) (string, error) {
	pwFile := os.Getenv("MON_DSN_PASSWORD_FILE")
	if pwFile == "" {
		return dsn, nil
	}
	b, err := os.ReadFile(pwFile) //nolint:gosec // pwFile sourced from MON_DSN_PASSWORD_FILE env, operator-controlled
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	pw := strings.TrimSpace(string(b))
	// pgx parses standard URL-form DSNs; insert password between user and host.
	// Expect form: postgres://user@host:port/db?...
	idx := strings.Index(dsn, "@")
	if idx < 0 {
		return "", errors.New("DSN missing user@ separator")
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 {
		return "", errors.New("DSN missing scheme")
	}
	user := dsn[scheme+3 : idx]
	rest := dsn[idx:]
	return dsn[:scheme+3] + user + ":" + pw + rest, nil
}

var _ = sql.ErrNoRows
