// Package safeexec runs external CLI tools with a hardened policy:
//   - explicit absolute path resolution against a fixed PATH (no $PATH inheritance),
//   - hard timeout via context,
//   - empty/scrubbed environment so callers do not accidentally leak secrets,
//   - bounded stdout capture so a chatty tool cannot exhaust agent memory.
//
// It deliberately does NOT use `sh -c` or any shell; arguments are passed as a
// fixed argv slice so quoting/expansion bugs cannot turn into arbitrary
// command execution.
package safeexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SafePath is a fixed lookup path for resolving binaries. We do not inherit the
// caller's $PATH because the agent runs as a long-lived service and may have a
// $PATH set by an operator/systemd unit that we don't want to trust.
var SafePath = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// MaxOutputBytes caps how much stdout we keep. Tools that exceed it return a
// truncation error so the caller can decide how to handle it.
const MaxOutputBytes = 4 << 20 // 4 MiB

var ErrOutputTruncated = errors.New("safeexec: output exceeded MaxOutputBytes")

// Run executes name with the given args, captures stdout, and returns it.
// The whole call is bounded by ctx (caller is responsible for setting a timeout).
//
// stderr is captured separately and only returned in the error path so that
// noisy tools (e.g. dnf check-update writes warnings to stderr) don't pollute
// the parsed payload.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if strings.ContainsAny(name, "/") {
		return nil, fmt.Errorf("safeexec: argument 0 must be a bare command name, got %q", name)
	}

	bin, err := lookPath(name)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C"} // deterministic output
	cmd.Stdin = nil

	stdout := &boundedBuffer{max: MaxOutputBytes}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		// Wrap with stderr tail so callers can log a useful diagnostic.
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 512 {
			tail = tail[len(tail)-512:]
		}
		return stdout.Bytes(), fmt.Errorf("safeexec %s: %w (stderr: %s)", name, err, tail)
	}
	if stdout.truncated {
		return stdout.Bytes(), ErrOutputTruncated
	}
	return stdout.Bytes(), nil
}

// RunWithTimeout is a convenience wrapper that applies an explicit timeout.
func RunWithTimeout(parent context.Context, d time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	return Run(ctx, name, args...)
}

// Available reports whether the named binary exists on SafePath.
func Available(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func lookPath(name string) (string, error) {
	for _, dir := range SafePath {
		p := dir + "/" + name
		fi, err := os.Stat(p)
		if err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("safeexec: %s not found on safe path", name)
}

type boundedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.truncated {
		return len(p), nil // discard, but pretend we wrote it
	}
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }

var _ io.Writer = (*boundedBuffer)(nil)
