package mail

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

func TestPostfixQueueCounter(t *testing.T) {
	t.Run("NonexistentRoot", func(t *testing.T) {
		result := postfixQueue("/nonexistent/path/that/does/not/exist")
		if result != nil {
			t.Errorf("expected nil for nonexistent root, got %+v", result)
		}
	})

	t.Run("ValidStructure", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create structure:
		// active/ with 1 file
		// deferred/ with 2 files + nested subdir with 2 files (4 total)
		// hold/ (empty)
		// incoming/ (doesn't exist)

		activeDir := filepath.Join(tmpDir, "active")
		deferredDir := filepath.Join(tmpDir, "deferred")
		holdDir := filepath.Join(tmpDir, "hold")

		if err := os.MkdirAll(activeDir, 0755); err != nil {
			t.Fatalf("failed to create active dir: %v", err)
		}
		if err := os.MkdirAll(deferredDir, 0755); err != nil {
			t.Fatalf("failed to create deferred dir: %v", err)
		}
		if err := os.MkdirAll(holdDir, 0755); err != nil {
			t.Fatalf("failed to create hold dir: %v", err)
		}

		// Create 1 file in active
		if err := os.WriteFile(filepath.Join(activeDir, "file1"), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file in active: %v", err)
		}

		// Create 2 files in deferred root
		if err := os.WriteFile(filepath.Join(deferredDir, "file1"), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file in deferred: %v", err)
		}
		if err := os.WriteFile(filepath.Join(deferredDir, "file2"), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file in deferred: %v", err)
		}

		// Create nested subdir in deferred with 2 files
		nestedDir := filepath.Join(deferredDir, "D")
		if err := os.MkdirAll(nestedDir, 0755); err != nil {
			t.Fatalf("failed to create nested deferred dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nestedDir, "file1"), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file in nested deferred: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nestedDir, "file2"), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file in nested deferred: %v", err)
		}

		// hold is empty, incoming doesn't exist

		result := postfixQueue(tmpDir)

		if result == nil {
			t.Fatal("expected non-nil result, got nil")
		}

		expectedQueue := &apitypes.PostfixQueue{
			Active:   1,
			Deferred: 4,
			Hold:     0,
			Incoming: 0,
			Total:    5,
		}

		if result.Active != expectedQueue.Active {
			t.Errorf("Active: expected %d, got %d", expectedQueue.Active, result.Active)
		}
		if result.Deferred != expectedQueue.Deferred {
			t.Errorf("Deferred: expected %d, got %d", expectedQueue.Deferred, result.Deferred)
		}
		if result.Hold != expectedQueue.Hold {
			t.Errorf("Hold: expected %d, got %d", expectedQueue.Hold, result.Hold)
		}
		if result.Incoming != expectedQueue.Incoming {
			t.Errorf("Incoming: expected %d, got %d", expectedQueue.Incoming, result.Incoming)
		}
		if result.Total != expectedQueue.Total {
			t.Errorf("Total: expected %d, got %d", expectedQueue.Total, result.Total)
		}
	})
}
