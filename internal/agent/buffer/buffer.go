// Package buffer implements the agent's disk-backed ingest spool.
//
// Spool semantics:
//
//   - Append() writes a single payload to its own file under the spool
//     directory: <unix-nanos>-<pid>.json.tmp then atomically renamed to
//     <unix-nanos>-<pid>.json. The rename guarantees a reader never sees
//     a partially-written file.
//   - Drain() iterates files in arrival order (lexicographic, which is
//     monotonic in the timestamp prefix), hands the bytes to a callback,
//     and removes the file only after the callback succeeds. On the
//     first callback error draining stops and the file remains on disk
//     for the next call — this is what makes ingest at-least-once across
//     agent restarts.
//   - enforceQuota() runs after every Append. When the total spool size
//     exceeds maxBytes the oldest files are unlinked until the quota is
//     satisfied. Data loss is preferred over unbounded disk growth.
//   - The spool directory is force-chmodded to 0700 on construction to
//     close a TOCTOU window if it pre-existed with looser perms
//     (AUDIT-033). Payload files are written 0600.
//
// All exported methods are safe for concurrent use. Internally a single
// mutex guards both writes and the drain loop; the spool is not designed
// for high concurrency (one agent tick at a time is the expected load).
package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Tunables / layout constants.
const (
	// spoolDirPerm is the mode enforced on the spool directory. Other
	// users on the host must not be able to read agent-key-authenticated
	// payloads sitting on disk.
	spoolDirPerm os.FileMode = 0o700

	// spoolFilePerm is the mode used for each .json payload file.
	spoolFilePerm os.FileMode = 0o600

	// spoolFileExt is the extension used for finalised payload files.
	// The matching .tmp suffix is used for in-flight writes.
	spoolFileExt = ".json"
	spoolTmpExt  = ".tmp"

	// timestampWidth is the zero-padded width of the unix-nanos prefix
	// used in spool filenames. 20 chars accommodates nanoseconds well
	// past the year 2200.
	timestampWidth = 20
)

// ErrEmptyDir is returned by New when the supplied directory is empty.
var ErrEmptyDir = errors.New("buffer: spool dir empty")

// Spool is an append-only on-disk queue for IngestRequest payloads. Each
// batch is written to its own .json.tmp then atomically renamed to a
// timestamped .json file. Older files are dropped when MaxBytes is
// exceeded.
type Spool struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
}

// New constructs a Spool at dir. The directory is created if missing and
// then force-chmodded to 0700. maxBytes <= 0 disables the disk quota.
func New(dir string, maxBytes int64) (*Spool, error) {
	if dir == "" {
		return nil, ErrEmptyDir
	}
	if err := os.MkdirAll(dir, spoolDirPerm); err != nil {
		return nil, fmt.Errorf("buffer: mkdir %q: %w", dir, err)
	}
	// MkdirAll is a no-op on existing directories and will not tighten
	// looser pre-existing permissions. Explicitly chmod to 0700 to close
	// a TOCTOU window where the spool dir could have been created earlier
	// with broader access (AUDIT-033).
	if err := os.Chmod(dir, spoolDirPerm); err != nil {
		return nil, fmt.Errorf("buffer: chmod %q: %w", dir, err)
	}
	return &Spool{dir: dir, maxBytes: maxBytes}, nil
}

// Append marshals payload as JSON and atomically commits it to the
// spool. The on-disk filename encodes nanosecond-arrival-time so the
// drain order is FIFO.
func (s *Spool) Append(payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := fmt.Sprintf("%0*d-%d%s", timestampWidth, time.Now().UnixNano(), os.Getpid(), spoolFileExt)
	tmp := filepath.Join(s.dir, name+spoolTmpExt)
	final := filepath.Join(s.dir, name)

	// audit 2026-05-12 §4.3.15: add O_EXCL. Under the single-mutex regime a
	// collision is impossible by construction (nanosecond clock + PID +
	// serialised Append), so this is defence-in-depth: if the invariant
	// ever breaks (clock skew on a virt-host save/restore, multi-process
	// abuse) we fail loud at open() rather than silently clobbering a
	// peer's in-flight payload.
	//nolint:gosec // path is agent-config-controlled, not user-tainted
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, spoolFilePerm)
	if err != nil {
		return fmt.Errorf("buffer: open tmp: %w", err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("buffer: encode payload: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("buffer: fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("buffer: close tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("buffer: rename: %w", err)
	}
	// audit 2026-05-12 §4.3.13: fsync the spool directory after the
	// rename. On ext4 with the default data=ordered the rename is durable
	// in practice, but the POSIX-portable "atomic durable write" pattern
	// requires fsync of the containing directory so the rename's metadata
	// hits stable storage. Best-effort: directory fsync isn't supported on
	// every filesystem (notably some FUSE backends), so we log-and-continue
	// rather than fail the whole append.
	if d, dErr := os.Open(s.dir); dErr == nil { //nolint:gosec // agent-config-controlled
		_ = d.Sync()
		_ = d.Close()
	}
	return s.enforceQuota()
}

// Drain calls fn for each spooled payload in arrival order. If fn
// returns nil, the payload file is deleted; on error draining stops and
// the file remains for the next call (at-least-once semantics).
func (s *Spool) Drain(fn func(raw []byte) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.list()
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(s.dir, e.Name())
		//nolint:gosec // path is agent-config-controlled, not user-tainted
		raw, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("buffer: read %s: %w", e.Name(), err)
		}
		if err := fn(raw); err != nil {
			return err
		}
		if err := os.Remove(full); err != nil {
			return fmt.Errorf("buffer: remove %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Len reports the number of payloads currently spooled. Returns 0 if
// the directory cannot be read (best-effort).
func (s *Spool) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := s.list()
	return len(entries)
}

// Close releases any resources held by the Spool. Files on disk are
// preserved across Close — they're the spill artefact the next process
// will pick up. Calling Close is currently a no-op; it exists so callers
// can defer it now and get free correctness if the implementation grows
// background goroutines later.
func (s *Spool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

// list returns the spooled payload files in FIFO order. Entries whose
// extension is not .json (e.g. half-written .json.tmp) are skipped so
// in-flight writes never appear to a drain pass.
func (s *Spool) list() ([]os.DirEntry, error) {
	all, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("buffer: readdir: %w", err)
	}
	out := make([]os.DirEntry, 0, len(all))
	for _, e := range all {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != spoolFileExt {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// enforceQuota deletes oldest files until total size fits maxBytes.
// A zero or negative maxBytes disables the quota.
func (s *Spool) enforceQuota() error {
	if s.maxBytes <= 0 {
		return nil
	}
	entries, err := s.list()
	if err != nil {
		return err
	}
	type spoolInfo struct {
		name string
		size int64
	}
	var total int64
	infos := make([]spoolInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, spoolInfo{e.Name(), fi.Size()})
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
