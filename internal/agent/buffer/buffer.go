package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Spool is an append-only on-disk queue for IngestRequest payloads. Each batch
// is written to its own gzip-less .ndjson.tmp then atomically renamed to a
// timestamped .ndjson file. Older files are dropped when MaxBytes is exceeded.
type Spool struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
}

func New(dir string, maxBytes int64) (*Spool, error) {
	if dir == "" {
		return nil, errors.New("spool dir empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Spool{dir: dir, maxBytes: maxBytes}, nil
}

func (s *Spool) Append(payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := fmt.Sprintf("%020d-%d.json", time.Now().UnixNano(), os.Getpid())
	tmp := filepath.Join(s.dir, name+".tmp")
	final := filepath.Join(s.dir, name)

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return s.enforceQuota()
}

// Drain calls fn for each spooled payload in arrival order. If fn returns nil,
// the payload file is deleted; on error draining stops and the file remains.
func (s *Spool) Drain(fn func(raw []byte) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.list()
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(s.dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		if err := fn(raw); err != nil {
			return err
		}
		if err := os.Remove(full); err != nil {
			return err
		}
	}
	return nil
}

func (s *Spool) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := s.list()
	return len(entries)
}

func (s *Spool) list() ([]os.DirEntry, error) {
	all, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]os.DirEntry, 0, len(all))
	for _, e := range all {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if filepath.Ext(n) != ".json" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// enforceQuota deletes oldest files until total size fits MaxBytes.
func (s *Spool) enforceQuota() error {
	if s.maxBytes <= 0 {
		return nil
	}
	entries, err := s.list()
	if err != nil {
		return err
	}
	var total int64
	infos := make([]struct {
		name string
		size int64
	}, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, struct {
			name string
			size int64
		}{e.Name(), fi.Size()})
		total += fi.Size()
	}
	for _, it := range infos {
		if total <= s.maxBytes {
			return nil
		}
		_ = os.Remove(filepath.Join(s.dir, it.name))
		total -= it.size
	}
	return nil
}

// Helpful for tests / debugging: parse the leading nanosecond timestamp.
func nameTimestamp(n string) (time.Time, bool) {
	if len(n) < 20 {
		return time.Time{}, false
	}
	ns, err := strconv.ParseInt(n[:20], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, ns), true
}

var _ = io.EOF // keep io import handy
var _ = nameTimestamp
