// Package serverlog captures slog records into an in-memory ring buffer so
// they can be exposed via the admin API. Old entries are overwritten when
// the buffer is full — server logs are not durable storage and operators who
// want long retention should ship the JSON stream off the host.
package serverlog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is a flattened slog record. Attrs is a flat map (no group support);
// the only structured-attr consumer is the API layer, which serializes it as
// a jsonb-style object to the client.
type Entry struct {
	Time  time.Time      `json:"time"`
	Level string         `json:"level"` // DEBUG | INFO | WARN | ERROR
	Msg   string         `json:"msg"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Buffer is a thread-safe ring of the last `size` entries. seq increments
// monotonically across writes so operators can detect drops between two
// snapshots.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	idx     int
	full    bool
	seq     uint64
	size    int
}

func NewBuffer(size int) *Buffer {
	if size <= 0 {
		size = 5000
	}
	return &Buffer{entries: make([]Entry, size), size: size}
}

func (b *Buffer) append(e Entry) {
	b.mu.Lock()
	b.entries[b.idx] = e
	b.idx = (b.idx + 1) % b.size
	if b.idx == 0 {
		b.full = true
	}
	b.seq++
	b.mu.Unlock()
}

// Filter narrows a snapshot. Empty fields are no-ops; case-insensitive
// substring on Msg + any string-typed Attr.
type Filter struct {
	Since    time.Time
	Until    time.Time
	MinLevel slog.Level
	Q        string
	HostID   string
}

// Snapshot copies entries matching f, newest first. The full ring is locked
// once and copied out so concurrent writers don't tear results.
func (b *Buffer) Snapshot(f Filter) (entries []Entry, totalSeq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	totalSeq = b.seq
	count := b.size
	if !b.full {
		count = b.idx
	}
	out := make([]Entry, 0, count)

	q := strings.ToLower(strings.TrimSpace(f.Q))

	// Walk newest → oldest. Newest entry sits at idx-1.
	for i := range count {
		pos := (b.idx - 1 - i + b.size) % b.size
		e := b.entries[pos]
		if !f.Since.IsZero() && e.Time.Before(f.Since) {
			continue
		}
		if !f.Until.IsZero() && e.Time.After(f.Until) {
			continue
		}
		if f.MinLevel != 0 && levelOrdinal(e.Level) < int(f.MinLevel) {
			continue
		}
		if f.HostID != "" {
			if v, ok := e.Attrs["host_id"]; !ok {
				continue
			} else if s, _ := v.(string); s != f.HostID {
				continue
			}
		}
		if q != "" && !matchSubstring(e, q) {
			continue
		}
		out = append(out, e)
	}
	return out, totalSeq
}

// matchSubstring checks msg + every string-typed attr value.
func matchSubstring(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.Msg), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Level), q) {
		return true
	}
	for _, v := range e.Attrs {
		if s, ok := v.(string); ok {
			if strings.Contains(strings.ToLower(s), q) {
				return true
			}
		}
	}
	return false
}

// Page applies offset/limit to an already-filtered slice. Caller decides
// pagination style (newest-first => slice from offset).
func Page(entries []Entry, offset, limit int) []Entry {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if offset >= len(entries) {
		return nil
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	return entries[offset:end]
}

// --- slog.Handler -----------------------------------------------------------

// Handler is a slog.Handler that writes through to `inner` (typically a JSON
// handler on stdout) and tees a flattened Entry into the buffer.
type Handler struct {
	inner slog.Handler
	buf   *Buffer
	attrs []slog.Attr
	group string
}

func NewHandler(inner slog.Handler, buf *Buffer) *Handler {
	return &Handler{inner: inner, buf: buf}
}

func (h *Handler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	attrs := map[string]any{}
	for _, a := range h.attrs {
		attrs[a.Key] = anyValue(a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = anyValue(a.Value)
		return true
	})
	h.buf.append(Entry{
		Time:  r.Time,
		Level: levelString(r.Level),
		Msg:   r.Message,
		Attrs: attrs,
	})
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	combined := append([]slog.Attr{}, h.attrs...)
	combined = append(combined, as...)
	return &Handler{inner: h.inner.WithAttrs(as), buf: h.buf, attrs: combined, group: h.group}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), buf: h.buf, attrs: h.attrs, group: name}
}

func anyValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time()
	case slog.KindAny:
		return v.Any()
	case slog.KindGroup:
		out := map[string]any{}
		for _, a := range v.Group() {
			out[a.Key] = anyValue(a.Value)
		}
		return out
	}
	return v.String()
}

func levelString(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

func levelOrdinal(s string) int {
	switch strings.ToUpper(s) {
	case "ERROR":
		return int(slog.LevelError)
	case "WARN", "WARNING":
		return int(slog.LevelWarn)
	case "INFO":
		return int(slog.LevelInfo)
	case "DEBUG":
		return int(slog.LevelDebug)
	}
	return int(slog.LevelInfo)
}

// LevelFromString parses "debug"/"info"/"warn"/"error" (case-insensitive)
// and falls back to slog.LevelInfo.
func LevelFromString(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	}
	return 0
}
