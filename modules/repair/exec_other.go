//go:build !windows

package repair

import (
	"errors"
)

// errUnsupported is returned when a repair command is attempted off Windows.
var errUnsupported = errors.New("repair: 当前平台不支持该操作")

// runCommand is a no-op off Windows: every repair action targets cmd.exe.
func runCommand(cmdline string) (string, error) { return "", errUnsupported }

// appendLogf formats a log line for callers that still want a textual trace.
func appendLogf(name, out string, err error) string {
	if err != nil {
		return "=== " + name + " ===\n" + err.Error()
	}
	return "=== " + name + " ===\n" + out
}
