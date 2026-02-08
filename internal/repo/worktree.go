package repo

import (
	"fmt"
	"os/exec"
	"strings"
)

// DirtyCheck checks if a working directory has uncommitted changes.
// Returns true if the working directory has modifications.
func DirtyCheck(workDir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status --porcelain: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}
