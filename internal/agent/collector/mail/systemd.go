package mail

import (
	"context"
	"errors"
	"strings"
)

// execFn is a function that executes a command and returns its output.
// It is injected so that tests can provide a fake implementation,
// while production code passes safeexec.Run.
type execFn func(ctx context.Context, name string, args ...string) ([]byte, error)

var ErrExecFailed = errors.New("exec failed")

// serviceState queries systemd for the state of a unit and returns:
//   - present: whether the unit was found (LoadState != "not-found")
//   - active: whether the unit is currently active (ActiveState == "active")
//   - sub: the SubState value (e.g. "running", "exited", "dead")
//
// If the exec function returns an error, present is false and sub is empty.
func serviceState(ctx context.Context, run execFn, unit string) (present bool, active bool, sub string) {
	out, err := run(ctx, "systemctl", "show", "-p", "LoadState", "-p", "ActiveState", "-p", "SubState", unit)
	if err != nil {
		return false, false, ""
	}

	// Parse the output: key=value lines
	states := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			states[parts[0]] = parts[1]
		}
	}

	loadState := states["LoadState"]
	activeState := states["ActiveState"]
	subState := states["SubState"]

	// A unit is present if LoadState is not "not-found"
	present = loadState != "not-found"
	// A unit is active if ActiveState is "active"
	active = activeState == "active"
	// Return the SubState value
	sub = subState

	return present, active, sub
}
