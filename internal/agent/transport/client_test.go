package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

func TestRegisterAndIngestRoundTrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/register":
			if got := r.Header.Get("Authorization"); got != "Bearer boot-tok" {
				t.Errorf("missing/incorrect bearer: %q", got)
			}
			body, _ := io.ReadAll(r.Body)
			var req apitypes.AgentRegisterRequest
			_ = json.Unmarshal(body, &req)
			if req.Hostname == "" {
				t.Error("hostname should not be empty")
			}
			_ = json.NewEncoder(w).Encode(apitypes.AgentRegisterResponse{
				AgentID:  "00000000-0000-0000-0000-000000000001",
				AgentKey: "mon_ag_test",
			})
		case "/v1/ingest":
			if got := r.Header.Get("Authorization"); got != "Bearer mon_ag_test" {
				t.Errorf("ingest bearer wrong: %q", got)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apitypes.IngestResponse{Accepted: true})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	c, err := New(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.Register(context.Background(), "boot-tok", apitypes.AgentRegisterRequest{
		Hostname: "test-host",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.AgentKey != "mon_ag_test" {
		t.Fatalf("unexpected key: %q", resp.AgentKey)
	}

	if err := c.Ingest(context.Background(), resp.AgentKey, []byte(`{"system":[]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestIngestRejectsNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer ts.Close()

	c, _ := New(ts.URL)
	err := c.Ingest(context.Background(), "wrong", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 401")
	}
}
