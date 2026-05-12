// Package ingestlog keeps the most recent agent ingest payloads in memory so
// admins can inspect exactly what each host sent. This is purely a debugging
// aid; long-term raw retention would need a separate table.
package ingestlog

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is one stored ingest. Payload is the canonical JSON the agent
// uploaded, copied so the buffer keeps no reference to request memory.
type Entry struct {
	Time      time.Time `json:"time"`
	HostID    string    `json:"host_id"`
	Hostname  string    `json:"hostname,omitempty"`
	SizeBytes int       `json:"size_bytes"`
	Payload   []byte    `json:"-"` // sent as RawMessage by the API layer
}

type Buffer struct {
	mu       sync.Mutex
	entries  []Entry
	idx      int
	full     bool
	size     int
	maxBytes int
}

func New(size, maxBytes int) *Buffer {
	if size <= 0 {
		size = 50
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB cap per entry
	}
	return &Buffer{entries: make([]Entry, size), size: size, maxBytes: maxBytes}
}

// Append stores a copy of payload. If payload exceeds maxBytes it's
// truncated; UI shows the truncation flag implicitly via SizeBytes vs
// len(Payload).
func (b *Buffer) Append(hostID uuid.UUID, hostname string, payload []byte) {
	originalSize := len(payload)
	redacted := Redact(payload)
	cp := redacted
	if len(cp) > b.maxBytes {
		cp = cp[:b.maxBytes]
	}
	stored := make([]byte, len(cp))
	copy(stored, cp)

	b.mu.Lock()
	b.entries[b.idx] = Entry{
		Time:      time.Now().UTC(),
		HostID:    hostID.String(),
		Hostname:  hostname,
		SizeBytes: originalSize,
		Payload:   stored,
	}
	b.idx = (b.idx + 1) % b.size
	if b.idx == 0 {
		b.full = true
	}
	b.mu.Unlock()
}

// Snapshot returns up to limit newest entries, optionally filtered by host.
func (b *Buffer) Snapshot(hostID string, limit int) []Entry {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	count := b.size
	if !b.full {
		count = b.idx
	}
	out := make([]Entry, 0, limit)
	for i := 0; i < count && len(out) < limit; i++ {
		pos := (b.idx - 1 - i + b.size) % b.size
		e := b.entries[pos]
		if hostID != "" && e.HostID != hostID {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Get returns a single entry by 0-based index from newest. Used to fetch the
// raw payload separately from the listing.
func (b *Buffer) Get(hostID string, idxFromNewest int) (Entry, bool) {
	if idxFromNewest < 0 {
		return Entry{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	count := b.size
	if !b.full {
		count = b.idx
	}
	seen := 0
	for i := range count {
		pos := (b.idx - 1 - i + b.size) % b.size
		e := b.entries[pos]
		if hostID != "" && e.HostID != hostID {
			continue
		}
		if seen == idxFromNewest {
			return e, true
		}
		seen++
	}
	return Entry{}, false
}
