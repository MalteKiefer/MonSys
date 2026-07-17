package mail

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// notFoundExec simulates systemctl reporting every unit as not-found.
func notFoundExec(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"), nil
}

// postfixOnlyExec simulates systemctl reporting postfix as loaded+active and
// all other units as not-found.
func postfixOnlyExec(_ context.Context, name string, args ...string) ([]byte, error) {
	// The last arg to "systemctl show" is the unit name.
	unit := ""
	if len(args) > 0 {
		unit = args[len(args)-1]
	}
	if unit == "postfix.service" {
		return []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"), nil
	}
	return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"), nil
}

// TestCollect_NonePresent verifies that when no mail unit is found,
// batch.Mail is left nil and Collect returns nil.
func TestCollect_NonePresent(t *testing.T) {
	c := &Collector{
		exec:       notFoundExec,
		spoolRoot:  t.TempDir(), // non-empty dir, but postfix won't be queried
		httpClient: nil,
		statURL:    "",
		dialHost:   "127.0.0.1",
	}

	batch := &apitypes.IngestRequest{}
	if err := c.Collect(context.Background(), batch); err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if batch.Mail != nil {
		t.Errorf("expected batch.Mail == nil when no units present, got %+v", batch.Mail)
	}
}

// TestCollect_PostfixPresent verifies that when postfix is active:
//   - batch.Mail is non-nil
//   - Services contains a postfix entry with Active=true
//   - Queue is populated (non-nil) because we point spoolRoot at a temp dir with files
//   - Ports contains entries for postfix ports (25, 587)
//   - rspamd path is NOT taken (rspamd not present)
func TestCollect_PostfixPresent(t *testing.T) {
	// Build a minimal spool directory with a couple of queue files.
	spoolRoot := t.TempDir()
	activeDir := filepath.Join(spoolRoot, "active")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	for _, name := range []string{"msg1", "msg2"} {
		f := filepath.Join(activeDir, name)
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatalf("write queue file: %v", err)
		}
	}

	c := &Collector{
		exec:       postfixOnlyExec,
		spoolRoot:  spoolRoot,
		httpClient: nil, // rspamd not present → never called
		statURL:    "",
		dialHost:   "127.0.0.1",
	}

	batch := &apitypes.IngestRequest{}
	if err := c.Collect(context.Background(), batch); err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if batch.Mail == nil {
		t.Fatal("expected batch.Mail != nil when postfix is present")
	}

	// Services must contain postfix entry.
	found := false
	for _, svc := range batch.Mail.Services {
		if svc.Name == "postfix" {
			found = true
			if !svc.Active {
				t.Errorf("postfix service: expected Active=true, got false")
			}
			if svc.SubState != "running" {
				t.Errorf("postfix service: expected SubState=running, got %q", svc.SubState)
			}
		}
	}
	if !found {
		t.Errorf("expected postfix in Services, got %+v", batch.Mail.Services)
	}

	// Queue must be non-nil (postfix is present and spoolRoot exists).
	if batch.Mail.Queue == nil {
		t.Error("expected Queue != nil when postfix is present")
	}

	// Ports must be populated with at least the postfix ports.
	if len(batch.Mail.Ports) == 0 {
		t.Error("expected Ports to be non-empty when postfix is present")
	}
	portNums := map[int]bool{}
	for _, pc := range batch.Mail.Ports {
		portNums[pc.Port] = true
	}
	for _, want := range []int{25, 587} {
		if !portNums[want] {
			t.Errorf("expected port %d in Ports, got %v", want, batch.Mail.Ports)
		}
	}

	// Rspamd must be nil because rspamd is not present.
	if batch.Mail.Rspamd != nil {
		t.Errorf("expected Rspamd == nil when rspamd not present, got %+v", batch.Mail.Rspamd)
	}
}
