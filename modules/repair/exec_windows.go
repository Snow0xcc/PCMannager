//go:build windows

package repair

import (
	"context"
	"fmt"
)

// runCommand executes cmdline through cmd /c under the catalogue action's
// execution budget and returns the trimmed combined output. The budget comes
// from actionTimeout(id): a hung command is killed via CommandContext and
// surfaces as a friendly timeout error naming the action.
func runCommand(id, cmdline string) (string, error) {
	return runActionBudgeted(id, actionTimeout(id), func(ctx context.Context) (string, error) {
		return runWithContext(ctx, "cmd", "/c", cmdline)
	})
}

// appendLogf formats a log line for the walk panel's log buffer.
func appendLogf(name, out string, err error) string {
	if err != nil {
		return fmt.Sprintf("=== %s ===\n%s\n[error] %v", name, out, err)
	}
	return fmt.Sprintf("=== %s ===\n%s", name, out)
}
