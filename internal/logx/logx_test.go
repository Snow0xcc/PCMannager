package logx

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingWriter always fails, mimicking stderr on a -H windowsgui build
// ("The handle is invalid").
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("handle is invalid")
}

// TestBestEffortDoesNotStopAtFirstError is the regression for the bug where
// io.MultiWriter(stderr, file) silently dropped EVERY record from the file as
// soon as stderr failed — which is exactly what happens on a Windows GUI build,
// the one build with no console to fall back on.
func TestBestEffortDoesNotStopAtFirstError(t *testing.T) {
	var file bytes.Buffer
	w := bestEffort([]io.Writer{failingWriter{}, &file})

	if _, err := w.Write([]byte("hello\n")); err == nil {
		t.Fatal("应返回失败 sink 的错误，以便调用方感知")
	}
	if got := file.String(); got != "hello\n" {
		t.Fatalf("文件 sink 应仍在写入, 实际 %q", got)
	}
}

// TestBestEffortAllSinksReceiveEveryRecord covers the plain multi-sink case.
func TestBestEffortAllSinksReceiveEveryRecord(t *testing.T) {
	var a, b bytes.Buffer
	w := bestEffort([]io.Writer{&a, &b})

	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("全部 sink 健康时不应报错: %v", err)
	}
	if a.String() != "x" || b.String() != "x" {
		t.Fatalf("每个 sink 都应收到记录: a=%q b=%q", a.String(), b.String())
	}
}

// TestNewWritesExtraFilesAndClosesThem ensures Options.ExtraFiles is honored and
// that the returned Closer closes every opened file.
func TestNewWritesExtraFilesAndClosesThem(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "logs", "a.log")
	extra := filepath.Join(dir, "program", "logs", "b.log")

	logger, closer, err := New(Options{
		Level:      "info",
		File:       primary,
		ExtraFiles: []string{extra, ""}, // 空路径应被忽略
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Info("hello", "k", "v")
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, path := range []string{primary, extra} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("日志文件应存在 %s: %v", path, err)
		}
		if !strings.Contains(string(data), "hello") {
			t.Fatalf("%s 应含写入的记录, 实际 %q", path, data)
		}
	}
}

// TestNewMissingDirIsNotFatal keeps the historical contract: an unwritable log
// location degrades logging rather than failing construction.
func TestNewMissingDirIsNotFatal(t *testing.T) {
	// 用一个不可能创建成目录的路径（已存在的文件当目录）。
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger, closer, err := New(Options{File: filepath.Join(blocker, "a.log")})
	if err != nil {
		t.Fatalf("日志目录不可用不应使 New 失败: %v", err)
	}
	logger.Info("still works")
	if closer != nil {
		t.Fatalf("没有文件被打开时 closer 应为 nil, 实际 %v", closer)
	}
}
