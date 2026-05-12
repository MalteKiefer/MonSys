package buffer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSpoolAppendAndDrain(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		if err := s.Append(map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Len(); got != 3 {
		t.Fatalf("want 3 spooled, got %d", got)
	}

	var seen []int
	if err := s.Drain(func(raw []byte) error {
		var m map[string]int
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		seen = append(seen, m["i"])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 drained, got %d", len(seen))
	}
	for i, v := range seen {
		if v != i {
			t.Fatalf("ordering broken: idx %d got %d", i, v)
		}
	}
	if got := s.Len(); got != 0 {
		t.Fatalf("want 0 after drain, got %d", got)
	}
}

func TestSpoolDrainStopsOnError(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, 1<<20)
	for i := range 3 {
		_ = s.Append(map[string]int{"i": i})
	}
	called := 0
	stop := errors.New("stop")
	err := s.Drain(func(raw []byte) error {
		called++
		if called == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("expected sentinel, got %v", err)
	}
	// First drained successfully, second errored => still 2 left on disk.
	if got := s.Len(); got != 2 {
		t.Fatalf("want 2 remaining, got %d", got)
	}
}

func TestSpoolQuotaEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	// Quota = 200 bytes; each payload ~ 30 bytes.
	s, _ := New(dir, 200)
	for range 50 {
		_ = s.Append(map[string]string{"x": strings.Repeat("a", 20)})
	}
	if got := s.Len(); got >= 50 {
		t.Fatalf("expected eviction, got %d files", got)
	}
}
