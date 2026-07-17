package api

// Integration tests for the mail ingest wiring and GET /v1/hosts/{id}/mail
// endpoint. These tests spin up a real TimescaleDB container so they are
// gated behind MON_TEST_DOCKER=1.
//
// Run with:
//
//	MON_TEST_DOCKER=1 go test ./internal/server/api/ -run Mail -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// mailTestImage mirrors the image used in the store-level tests so both
// layers test against the same DB version. Must stay in sync with
// internal/server/store/migrations_test.go:testImage.
const mailTestImage = "timescale/timescaledb:latest-pg16@sha256:15e00162766bd6f0019afaad4e57b850dcf882de5909bd7633899eebd4c03d57"

func mailDockerEnabled(t *testing.T) bool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainers-backed test under -short")
		return false
	}
	if os.Getenv("MON_TEST_DOCKER") != "1" {
		t.Skip("set MON_TEST_DOCKER=1 to run mail API tests against testcontainers")
		return false
	}
	return true
}

// startMailTestStore boots a fully-migrated *store.Store backed by a
// TimescaleDB container and returns a cleanup function.
func startMailTestStore(ctx context.Context, t *testing.T) (*store.Store, func()) {
	t.Helper()

	pgC, err := tcpostgres.Run(
		ctx,
		mailTestImage,
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
		t.Fatalf("get connection string: %v", err)
	}

	s, err := store.Open(ctx, dsn)
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

// TestMailIngestAndEndpoint verifies:
//  1. POST /v1/ingest with a Mail payload succeeds (200) for an enrolled host.
//  2. GET /v1/hosts/{id}/mail returns detected=true with the stored report.
//  3. GET /v1/hosts/{id}/mail for a host with no mail data returns detected=false.
func TestMailIngestAndEndpoint(t *testing.T) {
	if !mailDockerEnabled(t) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	st, cleanup := startMailTestStore(ctx, t)
	defer cleanup()

	// Build the full API server backed by the real store.
	srv := New(st)

	// --- Create an admin user and a session token for authenticated reads ---
	user, err := st.CreateUser(ctx, "admin@test.local", "S3cretPw!", "admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	sessToken, err := st.IssueSession(ctx, user, "test-agent", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}

	// --- Enroll a host and obtain its agent key ---
	bootstrapToken, err := st.CreateBootstrapToken(ctx, "mail-test", 10*time.Minute, user.Email)
	if err != nil {
		t.Fatalf("CreateBootstrapToken: %v", err)
	}

	regResp, err := st.RegisterAgent(ctx, bootstrapToken, apitypes.AgentRegisterRequest{
		Hostname:      "mail-host-01",
		MachineID:     "mail-machine-id-test-001",
		OS:            "linux",
		Kernel:        "6.6.0",
		Arch:          "amd64",
		Distro:        "Ubuntu 24.04",
		CPUModel:      "Test CPU",
		CPUCores:      2,
		RAMTotalBytes: 2 << 30,
		AgentVersion:  "v0.0.1",
	}, "127.0.0.1")
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	hostID := regResp.AgentID
	agentKey := regResp.AgentKey

	// --- POST /v1/ingest with a Mail payload ---
	now := time.Now().UTC().Truncate(time.Second)
	ingestBody := apitypes.IngestRequest{
		SnapshotAt: now,
		Mail: &apitypes.MailReport{
			Time: now,
			Queue: &apitypes.PostfixQueue{
				Active:   2,
				Deferred: 3,
				Hold:     0,
				Incoming: 0,
				Total:    5,
			},
			Rspamd: &apitypes.RspamdStat{
				Reachable:  true,
				Scanned:    200,
				Rejected:   4,
				Greylisted: 9,
				Learned:    80,
			},
		},
	}
	bodyBytes, err := json.Marshal(ingestBody)
	if err != nil {
		t.Fatalf("marshal ingest body: %v", err)
	}

	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/ingest", bytes.NewReader(bodyBytes))
	ingestReq.Header.Set("Content-Type", "application/json")
	ingestReq.Header.Set("Authorization", "Bearer "+agentKey)
	ingestRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(ingestRec, ingestReq)

	if ingestRec.Code != http.StatusOK {
		t.Fatalf("POST /v1/ingest: got status %d, want 200\nbody: %s",
			ingestRec.Code, ingestRec.Body.String())
	}

	// --- GET /v1/hosts/{id}/mail → detected=true ---
	mailReq := httptest.NewRequest(http.MethodGet, "/v1/hosts/"+hostID+"/mail", nil)
	mailReq.Header.Set("Authorization", "Bearer "+sessToken)
	mailRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(mailRec, mailReq)

	if mailRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/hosts/%s/mail: got status %d, want 200\nbody: %s",
			hostID, mailRec.Code, mailRec.Body.String())
	}

	var mailBody struct {
		Detected bool                 `json:"detected"`
		Report   *apitypes.MailReport `json:"report,omitempty"`
	}
	if err := json.NewDecoder(mailRec.Body).Decode(&mailBody); err != nil {
		t.Fatalf("decode mail response: %v", err)
	}
	if !mailBody.Detected {
		t.Error("GET /v1/hosts/{id}/mail: detected=false, want true")
	}
	if mailBody.Report == nil {
		t.Fatal("GET /v1/hosts/{id}/mail: report is nil, want non-nil")
	}
	if mailBody.Report.Queue == nil {
		t.Fatal("GET /v1/hosts/{id}/mail: report.queue is nil")
	}
	if mailBody.Report.Queue.Total != ingestBody.Mail.Queue.Total {
		t.Errorf("report.queue.total = %d, want %d",
			mailBody.Report.Queue.Total, ingestBody.Mail.Queue.Total)
	}
	if mailBody.Report.Rspamd == nil {
		t.Fatal("GET /v1/hosts/{id}/mail: report.rspamd is nil")
	}
	if mailBody.Report.Rspamd.Greylisted != ingestBody.Mail.Rspamd.Greylisted {
		t.Errorf("report.rspamd.greylisted = %d, want %d",
			mailBody.Report.Rspamd.Greylisted, ingestBody.Mail.Rspamd.Greylisted)
	}

	// --- Enroll a second host (no mail data) ---
	bootstrapToken2, err := st.CreateBootstrapToken(ctx, "mail-test-2", 10*time.Minute, user.Email)
	if err != nil {
		t.Fatalf("CreateBootstrapToken (host 2): %v", err)
	}
	regResp2, err := st.RegisterAgent(ctx, bootstrapToken2, apitypes.AgentRegisterRequest{
		Hostname:      "no-mail-host",
		MachineID:     "no-mail-machine-id-test-002",
		OS:            "linux",
		Kernel:        "6.6.0",
		Arch:          "amd64",
		Distro:        "Ubuntu 24.04",
		CPUModel:      "Test CPU",
		CPUCores:      2,
		RAMTotalBytes: 2 << 30,
		AgentVersion:  "v0.0.1",
	}, "127.0.0.1")
	if err != nil {
		t.Fatalf("RegisterAgent (host 2): %v", err)
	}
	noMailHostID := regResp2.AgentID

	// --- GET /v1/hosts/{noMailHostID}/mail → detected=false ---
	noMailReq := httptest.NewRequest(http.MethodGet, "/v1/hosts/"+noMailHostID+"/mail", nil)
	noMailReq.Header.Set("Authorization", "Bearer "+sessToken)
	noMailRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(noMailRec, noMailReq)

	if noMailRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/hosts/%s/mail (no-mail): got status %d, want 200\nbody: %s",
			noMailHostID, noMailRec.Code, noMailRec.Body.String())
	}

	var noMailBody struct {
		Detected bool                 `json:"detected"`
		Report   *apitypes.MailReport `json:"report,omitempty"`
	}
	if err := json.NewDecoder(noMailRec.Body).Decode(&noMailBody); err != nil {
		t.Fatalf("decode no-mail response: %v", err)
	}
	if noMailBody.Detected {
		t.Error("GET /v1/hosts/{id}/mail (no-mail): detected=true, want false")
	}
	if noMailBody.Report != nil {
		t.Errorf("GET /v1/hosts/{id}/mail (no-mail): report is non-nil, want nil; got %+v",
			noMailBody.Report)
	}
}
