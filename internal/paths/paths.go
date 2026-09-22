// Package paths resolves GoBox's data locations in one place so every module
// agrees on where things live (PRD §2.3).
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// AppName is the directory name used under the OS config root.
const AppName = "GoBox"

// DataDir returns the application data directory, creating it if needed.
//
// overridden is the optional user setting (app.data_dir); when set it wins.
// Windows resolves to %APPDATA%\GoBox, Linux to $XDG_CONFIG_HOME/GoBox.
func DataDir(overridden string) (string, error) {
	dir := overridden
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			// Last resort: keep the app functional rather than failing to boot.
			base = filepath.Join(os.TempDir(), AppName)
		}
		dir = filepath.Join(base, AppName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Ensure creates dir (and parents) and returns it.
func Ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// LogFile returns the path to the rolling log file inside the data dir.
func LogFile(dataDir string) string {
	return filepath.Join(dataDir, "logs", "gobox.log")
}

// ModuleDir returns (and creates) a module's private data directory.
func ModuleDir(dataDir, module string) (string, error) {
	return Ensure(filepath.Join(dataDir, module))
}

// IsWindows reports whether the current build targets Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }
