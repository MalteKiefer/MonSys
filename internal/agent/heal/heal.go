//go:build linux

// Package heal performs startup-time self-repair on the agent's runtime
// state: spool directory, key file parent dir, and their permissions. It is
// intentionally tolerant of pre-existing state — the goal is to reach a
// known-good baseline, not to clobber operator changes.
//
// Linux-only: relies on POSIX permission bits and the Linux default config
// paths emitted by internal/agent/config. A future heal_windows.go would
// validate NTFS ACLs against %ProgramData%\mon-agent instead.
package heal

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/MalteKiefer/MonSys/internal/agent/config"
)

// Verify ensures every file/dir the agent needs at runtime exists with safe
// permissions. Returns the first error that prevents the agent from running;
// non-fatal repairs are logged at INFO and not returned.
func Verify(cfg config.Config) error {
	if err := ensureDir(cfg.BufferDir, 0o700); err != nil {
		return fmt.Errorf("buffer_dir: %w", err)
	}
	if cfg.KeyFile != "" {
		if err := ensureDir(filepath.Dir(cfg.KeyFile), 0o700); err != nil {
			return fmt.Errorf("key_file parent: %w", err)
		}
		if err := tightenIfTooOpen(cfg.KeyFile, 0o400); err != nil {
			slog.Warn("could not tighten key_file permissions", "path", cfg.KeyFile, "err", err)
		}
	}
	return nil
}

func ensureDir(path string, mode fs.FileMode) error {
	if path == "" {
		return errors.New("empty path")
	}
	st, err := os.Stat(path)
	if err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", path)
		}
		// Re-tighten if too permissive — covers operators copying state from
		// a less-locked-down host.
		if st.Mode().Perm()&0o077 != 0 {
			if chmodErr := os.Chmod(path, mode); chmodErr == nil {
				slog.Info("tightened directory permissions", "path", path, "mode", mode.String())
			}
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	slog.Info("created missing directory", "path", path, "mode", mode.String())
	return nil
}

// tightenIfTooOpen narrows a file's mode bits *only* if currently too open;
// never widens. Missing files are not an error here — bootstrap may not have
// run yet.
func tightenIfTooOpen(path string, target fs.FileMode) error {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if st.IsDir() {
		return errors.New("expected a file")
	}
	cur := st.Mode().Perm()
	if cur&0o077 == 0 && cur&^target == 0 {
		return nil
	}
	return os.Chmod(path, target)
}
