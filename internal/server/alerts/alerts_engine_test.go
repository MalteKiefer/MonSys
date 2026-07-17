// Engine integration tests — require a live TimescaleDB container.
//
// Gated behind MON_TEST_DOCKER=1 (same contract as the store migration tests).
// Run with:
//
//	MON_TEST_DOCKER=1 go test ./internal/server/alerts/ -run '(Mail|Metric)' -v
package alerts

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MalteKiefer/MonSys/internal/server/store/migrations"
)

// testImage tracks the digest used in deploy/docker-compose.yaml and in
// internal/server/store/migrations_test.go.  Bumping one must bump all three.
const engineTestImage = "timescale/timescaledb:latest-pg16@sha256:15e00162766bd6f0019afaad4e57b850dcf882de5909bd7633899eebd4c03d57"

// dockerEnabledEngine returns true when the host opted into containerised tests.
func dockerEnabledEngine(t *testing.T) bool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainers-backed test under -short")
		return false
	}
	if os.Getenv("MON_TEST_DOCKER") != "1" {
		t.Skip("set MON_TEST_DOCKER=1 to run engine integration tests")
		return false
	}
	return true
}

// startEngineDB boots a TimescaleDB container, runs all migrations, and
// returns a pgxpool.Pool ready for the Engine.  cleanup terminates the
// container and closes the pool.
func startEngineDB(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	pgC, err := tcpostgres.Run(
		ctx,
		engineTestImage,
		tcpostgres.WithDatabase("mon"),
		tcpostgres.WithUsername("mon"),
		tcpostgres.WithPassword("monpw"),
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
		t.Fatalf("parse pool config: %v", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		_ = pgC.Terminate(ctx)
		t.Fatalf("open pgxpool: %v", err)
	}

	// Run migrations using goose + the embedded FS (same as store.MigrateUp).
	if err := goose.SetDialect("postgres"); err != nil {
		pool.Close()
		_ = pgC.Terminate(ctx)
		t.Fatalf("goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	db := stdlib.OpenDB(*cfg.ConnConfig)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		_ = db.Close()
		pool.Close()
		_ = pgC.Terminate(ctx)
		t.Fatalf("goose up: %v", err)
	}
	_ = db.Close()

	cleanup := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return pool, cleanup
}

// insertEngineHost inserts a minimal host row and returns its ID.
func insertEngineHost(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hostname string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, first_seen_at, last_seen_at)
		VALUES ($1, $2, now(), now())`,
		id, hostname)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	return id
}

// TestMetricMailQueueDeferred verifies that a metric_threshold rule with
// metric=mail_queue_deferred fires when the latest metrics_mail row for a host
// has queue_deferred exceeding the threshold value.
func TestMetricMailQueueDeferred(t *testing.T) {
	if !dockerEnabledEngine(t) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, cleanup := startEngineDB(ctx, t)
	defer cleanup()

	hostID := insertEngineHost(ctx, t, pool, "mail-alert-test-host")

	// Seed a metrics_mail row: queue_deferred=15 exceeds the threshold of 10.
	_, err := pool.Exec(ctx, `
		INSERT INTO metrics_mail (time, host_id, queue_active, queue_deferred, queue_hold, queue_total)
		VALUES (now(), $1, 0, 15, 0, 15)`,
		hostID)
	if err != nil {
		t.Fatalf("insert metrics_mail: %v", err)
	}

	// Insert a notification_rule for metric_threshold mail_queue_deferred > 10.
	ruleID := uuid.New()
	params := fmt.Sprintf(
		`{"metric":"mail_queue_deferred","comparator":">","value":10,"window_sec":120}`,
	)
	_, err = pool.Exec(ctx, `
		INSERT INTO notification_rules
		    (id, name, enabled, condition_type, condition_params, channel_ids,
		     severity, throttle_sec, repeat_interval_sec, notify_on_resolve,
		     target_host_ids, target_tags, target_group_ids)
		VALUES ($1,$2,true,'metric_threshold',$3::jsonb,'{}','warning',0,0,false,'{}','{}','{}')`,
		ruleID, "test-mail-queue-deferred", params)
	if err != nil {
		t.Fatalf("insert notification_rule: %v", err)
	}

	// Construct the engine backed by the test pool and run the evaluator once.
	eng := New(pool, nil, nil)
	eng.evalMetricThreshold(ctx)

	// Assert: alert_history should contain exactly one row for our dedup key.
	expectedDedup := fmt.Sprintf("metric_threshold:%s:mail_queue_deferred", hostID)
	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_history WHERE dedup_key = $1`,
		expectedDedup).Scan(&count)
	if err != nil {
		t.Fatalf("query alert_history: %v", err)
	}
	if count != 1 {
		t.Errorf("alert_history rows for dedup %q: got %d, want 1", expectedDedup, count)
	}

	// Also assert: alert_state must have an open (non-resolved) row.
	var stateCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_state WHERE dedup_key = $1 AND resolved_at IS NULL`,
		expectedDedup).Scan(&stateCount)
	if err != nil {
		t.Fatalf("query alert_state: %v", err)
	}
	if stateCount != 1 {
		t.Errorf("alert_state open rows for dedup %q: got %d, want 1", expectedDedup, stateCount)
	}
}

// TestMailServiceDown verifies that a mail_service_down rule fires for a host
// whose latest host_mail_status report contains an inactive service, and does
// not fire for a host where all services are active.
func TestMailServiceDown(t *testing.T) {
	if !dockerEnabledEngine(t) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, cleanup := startEngineDB(ctx, t)
	defer cleanup()

	// Host 1: has postfix inactive → should fire.
	downHostID := insertEngineHost(ctx, t, pool, "mail-down-host")

	// Host 2: all services active → should NOT fire.
	upHostID := insertEngineHost(ctx, t, pool, "mail-up-host")

	// Seed host_mail_status for the down host: postfix inactive.
	downReport := `{"time":"2026-01-01T00:00:00Z","services":[{"name":"postfix","active":false,"sub_state":"dead"},{"name":"dovecot","active":true,"sub_state":"running"}]}`
	_, err := pool.Exec(ctx,
		`INSERT INTO host_mail_status (host_id, updated_at, report) VALUES ($1, now(), $2::jsonb)`,
		downHostID, downReport)
	if err != nil {
		t.Fatalf("insert host_mail_status (down host): %v", err)
	}

	// Seed host_mail_status for the up host: all services active.
	upReport := `{"time":"2026-01-01T00:00:00Z","services":[{"name":"postfix","active":true,"sub_state":"running"},{"name":"dovecot","active":true,"sub_state":"running"}]}`
	_, err = pool.Exec(ctx,
		`INSERT INTO host_mail_status (host_id, updated_at, report) VALUES ($1, now(), $2::jsonb)`,
		upHostID, upReport)
	if err != nil {
		t.Fatalf("insert host_mail_status (up host): %v", err)
	}

	// Insert a mail_service_down notification rule (targets all hosts).
	ruleID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO notification_rules
		    (id, name, enabled, condition_type, condition_params, channel_ids,
		     severity, throttle_sec, repeat_interval_sec, notify_on_resolve,
		     target_host_ids, target_tags, target_group_ids)
		VALUES ($1,$2,true,'mail_service_down','{}','{}','critical',0,0,false,'{}','{}','{}')`,
		ruleID, "test-mail-service-down")
	if err != nil {
		t.Fatalf("insert notification_rule: %v", err)
	}

	// Run the evaluator.
	eng := New(pool, nil, nil)
	rules, err := eng.loadRules(ctx, "mail_service_down")
	if err != nil {
		t.Fatalf("loadRules: %v", err)
	}
	eng.evalMailServiceDown(ctx, rules)

	// Assert: alert fires for down host + postfix service.
	expectedDedup := fmt.Sprintf("mail_service_down:%s:postfix", downHostID)
	var histCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_history WHERE dedup_key = $1`,
		expectedDedup).Scan(&histCount)
	if err != nil {
		t.Fatalf("query alert_history (down host): %v", err)
	}
	if histCount != 1 {
		t.Errorf("alert_history rows for dedup %q: got %d, want 1", expectedDedup, histCount)
	}

	var stateCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_state WHERE dedup_key = $1 AND resolved_at IS NULL`,
		expectedDedup).Scan(&stateCount)
	if err != nil {
		t.Fatalf("query alert_state (down host): %v", err)
	}
	if stateCount != 1 {
		t.Errorf("alert_state open rows for dedup %q: got %d, want 1", expectedDedup, stateCount)
	}

	// Assert: dovecot on down host is active → no alert fired for it.
	dovecotDedup := fmt.Sprintf("mail_service_down:%s:dovecot", downHostID)
	var dovecotCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_history WHERE dedup_key = $1`,
		dovecotDedup).Scan(&dovecotCount)
	if err != nil {
		t.Fatalf("query alert_history (dovecot): %v", err)
	}
	if dovecotCount != 0 {
		t.Errorf("alert_history rows for active dovecot dedup %q: got %d, want 0", dovecotDedup, dovecotCount)
	}

	// Assert: up host has no alert fired at all.
	var upCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM alert_history WHERE dedup_key LIKE $1`,
		fmt.Sprintf("mail_service_down:%s:%%", upHostID)).Scan(&upCount)
	if err != nil {
		t.Fatalf("query alert_history (up host): %v", err)
	}
	if upCount != 0 {
		t.Errorf("alert_history rows for up-host dedup prefix: got %d, want 0", upCount)
	}
}
