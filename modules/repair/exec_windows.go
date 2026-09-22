//go:build windows

package repair

import (
	"fmt"
	"os/exec"
	"strings"
)

// runCommand executes cmdline through cmd /c and returns the combined output.
func runCommand(cmdline string) (string, error) {
	cmd := exec.Command("cmd", "/c", cmdline)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// appendLogf formats a log line for the walk panel's log buffer.
func appendLogf(name, out string, err error) string {
	if err != nil {
		return fmt.Sprintf("=== %s ===\n%s\n[error] %v", name, out, err)
	}
	return fmt.Sprintf("=== %s ===\n%s", name, out)
}
