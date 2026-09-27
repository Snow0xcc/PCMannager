package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogFileShape pins the data-directory log layout the panel and docs refer to.
func TestLogFileShape(t *testing.T) {
	got := LogFile("/data")
	want := filepath.Join("/data", "logs", LogFileName)
	if got != want {
		t.Fatalf("LogFile = %q, 期望 %q", got, want)
	}
}

// TestExecutableLogFileLivesBesideTheProgram verifies the "program directory
// logs" contract: the log sits under <exeDir>/logs, not under the data dir, so
// a GUI build with no console can still be inspected from where it was installed.
func TestExecutableLogFileLivesBesideTheProgram(t *testing.T) {
	exeDir, err := ExecutableDir()
	if err != nil {
		t.Fatalf("ExecutableDir: %v", err)
	}
	logPath, err := ExecutableLogFile()
	if err != nil {
		t.Fatalf("ExecutableLogFile: %v", err)
	}

	if got := filepath.Dir(filepath.Dir(logPath)); got != exeDir {
		t.Fatalf("日志应位于 <exeDir>/logs 下: exeDir=%q, logDir=%q", exeDir, got)
	}
	if !strings.HasSuffix(logPath, LogFileName) {
		t.Fatalf("应以 %s 结尾, 实际 %q", LogFileName, logPath)
	}
}

// TestExecutableDirMatchesRunningBinary makes sure the directory is resolved
// from the executable and not from the (unreliable) working directory.
func TestExecutableDirMatchesRunningBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	dir, err := ExecutableDir()
	if err != nil {
		t.Fatalf("ExecutableDir: %v", err)
	}
	if dir != filepath.Dir(exe) {
		t.Fatalf("ExecutableDir = %q, 期望 %q", dir, filepath.Dir(exe))
	}
}
