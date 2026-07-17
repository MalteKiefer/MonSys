package mail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRspamdStat(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"scanned":100,"learned":2,"actions":{"reject":3,"greylist":7}}`))
		}))
		defer server.Close()

		result := rspamdStat(context.Background(), http.DefaultClient, server.URL)

		if result == nil {
			t.Fatal("expected non-nil result, got nil")
		}
		if !result.Reachable {
			t.Errorf("Reachable: expected true, got %v", result.Reachable)
		}
		if result.Scanned != 100 {
			t.Errorf("Scanned: expected 100, got %d", result.Scanned)
		}
		if result.Learned != 2 {
			t.Errorf("Learned: expected 2, got %d", result.Learned)
		}
		if result.Rejected != 3 {
			t.Errorf("Rejected: expected 3, got %d", result.Rejected)
		}
		if result.Greylisted != 7 {
			t.Errorf("Greylisted: expected 7, got %d", result.Greylisted)
		}
	})

	t.Run("ServerError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		result := rspamdStat(context.Background(), http.DefaultClient, server.URL)

		if result == nil {
			t.Fatal("expected non-nil result, got nil")
		}
		if result.Reachable {
			t.Errorf("Reachable: expected false, got %v", result.Reachable)
		}
		if result.Scanned != 0 {
			t.Errorf("Scanned: expected 0, got %d", result.Scanned)
		}
		if result.Learned != 0 {
			t.Errorf("Learned: expected 0, got %d", result.Learned)
		}
		if result.Rejected != 0 {
			t.Errorf("Rejected: expected 0, got %d", result.Rejected)
		}
		if result.Greylisted != 0 {
			t.Errorf("Greylisted: expected 0, got %d", result.Greylisted)
		}
	})
}
