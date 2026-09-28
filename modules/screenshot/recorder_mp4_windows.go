//go:build windows

package screenshot

import (
	"os/exec"
	"syscall"
)

// hideCmdWindowOS suppresses the console window an ffmpeg child would open on
// Windows (the recorder runs inside a GUI-subsystem binary).
func hideCmdWindowOS(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
