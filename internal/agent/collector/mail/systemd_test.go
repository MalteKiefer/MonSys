package mail

import (
	"context"
	"testing"
)

func TestServiceState_LoadedActive(t *testing.T) {
	// Fake execFn that returns a loaded, active, running unit
	fakeExec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"), nil
	}

	present, active, sub := serviceState(context.Background(), fakeExec, "test.service")

	if !present {
		t.Error("expected present=true, got false")
	}
	if !active {
		t.Error("expected active=true, got false")
	}
	if sub != "running" {
		t.Errorf("expected sub=running, got %q", sub)
	}
}

func TestServiceState_NotFound(t *testing.T) {
	// Fake execFn that returns a not-found unit
	fakeExec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"), nil
	}

	present, active, sub := serviceState(context.Background(), fakeExec, "missing.service")

	if present {
		t.Error("expected present=false for not-found unit, got true")
	}
	if active {
		t.Error("expected active=false, got true")
	}
	if sub != "dead" {
		t.Errorf("expected sub=dead, got %q", sub)
	}
}

func TestServiceState_ExecError(t *testing.T) {
	// Fake execFn that returns an error
	fakeExec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, ErrExecFailed
	}

	present, active, sub := serviceState(context.Background(), fakeExec, "broken.service")

	if present {
		t.Error("expected present=false on exec error, got true")
	}
	if active {
		t.Error("expected active=false, got true")
	}
	if sub != "" {
		t.Errorf("expected sub='', got %q", sub)
	}
}
