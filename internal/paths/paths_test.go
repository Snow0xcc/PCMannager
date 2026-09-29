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

// TestConfigDirFromEnvUnsetIsNeutral 守护“没设环境变量就等于没覆盖”，
// 否则默认启动路径会被意外改写。
func TestConfigDirFromEnvUnsetIsNeutral(t *testing.T) {
	t.Setenv(ConfigDirEnvVar, "")
	dir, warn := ConfigDirFromEnv()
	if dir != "" || warn != "" {
		t.Fatalf("未设置时应返回空: dir=%q warn=%q", dir, warn)
	}
}

// TestConfigDirFromEnvAcceptsDirAndFile 守护文档中写明的两种取值：目录本身，
// 或配置文件路径（取其父目录）。
func TestConfigDirFromEnvAcceptsDirAndFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv(ConfigDirEnvVar, root)
	if dir, warn := ConfigDirFromEnv(); dir != root || warn != "" {
		t.Fatalf("目录取值: dir=%q warn=%q, 期望 dir=%q", dir, warn, root)
	}

	file := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(file, []byte("app:\n"), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	t.Setenv(ConfigDirEnvVar, file)
	if dir, warn := ConfigDirFromEnv(); dir != root || warn != "" {
		t.Fatalf("文件取值应回退到父目录: dir=%q warn=%q, 期望 dir=%q", dir, warn, root)
	}
}

// TestConfigDirFromEnvReportsMissingPath 守护“不能静默忽略”：路径不存在时必须
// 给出警告，否则应用会悄悄退回系统默认目录，排查时毫无线索。
func TestConfigDirFromEnvReportsMissingPath(t *testing.T) {
	t.Setenv(ConfigDirEnvVar, filepath.Join(t.TempDir(), "nope"))
	dir, warn := ConfigDirFromEnv()
	if dir != "" {
		t.Fatalf("路径不存在时不应返回目录, 实际 %q", dir)
	}
	if warn == "" {
		t.Fatal("路径不存在时应返回警告")
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
