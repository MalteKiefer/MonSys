package mail

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// postfixQueue counts regular files in postfix queue directories under spoolRoot.
// Returns nil if spoolRoot does not exist. Counts files recursively in each of
// active, deferred, hold, and incoming subdirectories. Ignores missing subdirectories
// (counts 0). Does not follow symlinks.
func postfixQueue(spoolRoot string) *apitypes.PostfixQueue {
	// Check if spoolRoot exists
	if _, err := os.Stat(spoolRoot); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// Other errors (permission denied, etc.) also return nil
		return nil
	}

	queueDirs := []string{"active", "deferred", "hold", "incoming"}
	counts := make(map[string]int)

	for _, qdir := range queueDirs {
		qpath := filepath.Join(spoolRoot, qdir)
		counts[qdir] = countRegularFiles(qpath)
	}

	total := counts["active"] + counts["deferred"] + counts["hold"] + counts["incoming"]

	return &apitypes.PostfixQueue{
		Active:   counts["active"],
		Deferred: counts["deferred"],
		Hold:     counts["hold"],
		Incoming: counts["incoming"],
		Total:    total,
	}
}

// countRegularFiles recursively counts regular files in a directory tree.
// Returns 0 if the directory does not exist. Does not follow symlinks.
func countRegularFiles(dirPath string) int {
	count := 0

	// Use filepath.WalkDir for efficient directory traversal without following symlinks.
	// WalkDir doesn't follow symlinks by default.
	err := filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// If the directory doesn't exist or can't be read, stop gracefully
			return nil
		}

		// Count only regular files, not directories
		if !d.IsDir() {
			count++
		}

		return nil
	})

	// If WalkDir fails entirely (e.g., directory doesn't exist), return 0
	if err != nil {
		return 0
	}

	return count
}
