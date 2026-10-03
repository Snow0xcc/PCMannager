//go:build !windows

package screenshot

import "os/exec"

// hideCmdWindowOS is a no-op off Windows: there is no console to hide.
func hideCmdWindowOS(*exec.Cmd) {}
