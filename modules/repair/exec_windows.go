//go:build windows

package repair

import (
	"errors"
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

// interruptedExit is the status a console program reports after Ctrl+C /
// CTRL_CLOSE_EVENT (STATUS_CONTROL_C_EXIT, 0xC000013A).
const interruptedExit = 0xC000013A

// isInterrupted reports whether err is "the user stopped it", which callers
// treat as a normal ending rather than a failure.
func isInterrupted(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	// Go reports the raw status; on Windows an unsigned 0xC000013A surfaces as
	// the negative equivalent (-1073741510).
	code := exitErr.ExitCode()
	return uint32(code) == interruptedExit
}

// appendLogf formats a log line for the walk panel's log buffer.
func appendLogf(name, out string, err error) string {
	if err != nil {
		return fmt.Sprintf("=== %s ===\n%s\n[error] %v", name, out, err)
	}
	return fmt.Sprintf("=== %s ===\n%s", name, out)
}
