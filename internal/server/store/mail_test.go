package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// startTimescaleStore boots a fully-migrated *Store backed by a TimescaleDB
// container. Cleanup must be called when the test is done.
func startTimescaleStore(ctx context.Context, t *testing.T) (*Store, func()) {
	t.Helper()

	pgC, err := tcpostgres.Run(
		ctx,
		testImage,
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

	s, err := Open(ctx, dsn)
	if err != nil {
		_ = pgC.Terminate(ctx)
		t.Fatalf("store.Open: %v", err)
	}

	if err := s.MigrateUp(ctx); err != nil {
		s.Close()
		_ = pgC.Terminate(ctx)
		t.Fatalf("MigrateUp: %v", err)
	}

	cleanup := func() {
		s.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return s, cleanup
}

// insertTestHost inserts a minimal host row and returns its ID.
func insertTestHost(ctx context.Context, t *testing.T, s *Store, hostname string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO hosts (id, hostname, first_seen_at, last_seen_at)
		VALUES ($1, $2, now(), now())`,
		id, hostname)
	if err != nil {
		t.Fatalf("insert test host: %v", err)
	}
	return id
}

func TestMailStore(t *testing.T) {
	if !dockerEnabled(t) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	s, cleanup := startTimescaleStore(ctx, t)
	defer cleanup()

	hostID := insertTestHost(ctx, t, s, "mail-test-host")

	now := time.Now().UTC().Truncate(time.Second)
	report := apitypes.MailReport{
		Time: now,
		Queue: &apitypes.PostfixQueue{
			Active:   1,
			Deferred: 5,
			Hold:     2,
			Incoming: 0,
			Total:    8,
		},
		Rspamd: &apitypes.RspamdStat{
			Reachable:  true,
			Scanned:    100,
			Rejected:   3,
			Greylisted: 7,
			Learned:    50,
		},
	}

	// SaveMailReport must succeed.
	if err := s.SaveMailReport(ctx, hostID, report); err != nil {
		t.Fatalf("SaveMailReport: %v", err)
	}

	// MailStatus must return the report we just saved.
	got, found, err := s.MailStatus(ctx, hostID)
	if err != nil {
		t.Fatalf("MailStatus: %v", err)
	}
	if !found {
		t.Fatal("MailStatus: expected found=true, got false")
	}
	if got.Queue == nil {
		t.Fatal("MailStatus: Queue is nil")
	}
	if got.Queue.Total != report.Queue.Total {
		t.Errorf("Queue.Total: got %d, want %d", got.Queue.Total, report.Queue.Total)
	}
	if got.Rspamd == nil {
		t.Fatal("MailStatus: Rspamd is nil")
	}
	if got.Rspamd.Greylisted != report.Rspamd.Greylisted {
		t.Errorf("Rspamd.Greylisted: got %d, want %d", got.Rspamd.Greylisted, report.Rspamd.Greylisted)
	}

	// MailQueueSeries must return exactly one point with the correct values.
	points, err := s.MailQueueSeries(ctx, hostID, now.Add(-time.Second))
	if err != nil {
		t.Fatalf("MailQueueSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("MailQueueSeries: got %d points, want 1", len(points))
	}
	if points[0].Total != report.Queue.Total {
		t.Errorf("MailQueueSeries[0].Total: got %d, want %d", points[0].Total, report.Queue.Total)
	}
	if points[0].Deferred != report.Queue.Deferred {
		t.Errorf("MailQueueSeries[0].Deferred: got %d, want %d", points[0].Deferred, report.Queue.Deferred)
	}

	// Unknown host must return found=false without error.
	_, found2, err := s.MailStatus(ctx, uuid.New())
	if err != nil {
		t.Fatalf("MailStatus unknown host: %v", err)
	}
	if found2 {
		t.Error("MailStatus unknown host: expected found=false, got true")
	}
}
