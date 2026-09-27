package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotationKeepsFileBounded 守护核心需求：日志文件不会无限增长。
// 写入量远超上限后，主文件必须已被轮转（体积重新变小），并产生 .1 备份。
func TestRotationKeepsFileBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "gobox.log")

	const maxBytes = 512
	rw, err := newRotatingWriter(path, maxBytes, 3)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	// 写入 10 倍上限的数据，每行 64 字节。
	line := strings.Repeat("x", 63) + "\n"
	for i := 0; i < maxBytes/64*10; i++ {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("主日志文件应存在: %v", err)
	}
	if fi.Size() > maxBytes {
		t.Fatalf("主日志文件 %d 字节，超过上限 %d（未轮转）", fi.Size(), maxBytes)
	}
	// 必须产生备份，否则上面的体积只能说明写入丢失了。
	backup := backupPath(path, 1)
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("应存在轮转备份 %s: %v", backup, err)
	}
}

// TestRotationCapsBackupCount 守护备份数量上限，防止磁盘占用无界。
func TestRotationCapsBackupCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gobox.log")

	const (
		maxBytes   = 256
		maxBackups = 2
	)
	rw, err := newRotatingWriter(path, maxBytes, maxBackups)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	line := strings.Repeat("y", 63) + "\n"
	// 足够产生远多于 maxBackups 次轮转的写入量。
	for i := 0; i < maxBytes/64*40; i++ {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	for i := 1; i <= maxBackups; i++ {
		if _, err := os.Stat(backupPath(path, i)); err != nil {
			t.Errorf("备份 .%d 应存在: %v", i, err)
		}
	}
	// 第 maxBackups+1 个必须已被删除。
	if _, err := os.Stat(backupPath(path, maxBackups+1)); !os.IsNotExist(err) {
		t.Errorf("备份 .%d 应已被删除，err=%v", maxBackups+1, err)
	}
}

// TestRotationPreservesAllRecords 守护轮转不丢数据：主文件与所有备份拼接后，
// 应按顺序包含全部写入的行。
func TestRotationPreservesAllRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gobox.log")

	const maxBytes = 128
	rw, err := newRotatingWriter(path, maxBytes, 10)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	// 每行唯一且长度确定，便于逐行校验。
	var want []string
	for i := 0; i < 20; i++ {
		line := "line-" + itoaTest(i) + strings.Repeat("-", 20) + "\n"
		want = append(want, line)
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// 备份编号越大越旧，拼接顺序为 N..1 再接主文件。
	var got strings.Builder
	for i := 10; i >= 1; i-- {
		if b, err := os.ReadFile(backupPath(path, i)); err == nil {
			got.Write(b)
		}
	}
	if b, err := os.ReadFile(path); err != nil {
		t.Fatalf("读取主文件: %v", err)
	} else {
		got.Write(b)
	}

	text := got.String()
	for _, line := range want {
		if !strings.Contains(text, line) {
			t.Fatalf("轮转后丢失记录 %q", strings.TrimSpace(line))
		}
	}
	// 顺序性：记录在拼接结果中的位置必须单调递增。
	prev := -1
	for _, line := range want {
		idx := strings.Index(text, line)
		if idx < 0 {
			t.Fatalf("缺少记录 %q", strings.TrimSpace(line))
		}
		if idx < prev {
			t.Fatalf("记录顺序错乱: %q 出现在 %d，前一条在 %d", strings.TrimSpace(line), idx, prev)
		}
		prev = idx
	}
}

// TestRotationDefaultsWhenUnset 守护 0 值自动取默认上限，
// 这样 Options 未设置时也不会退化成无上限的日志文件。
func TestRotationDefaultsWhenUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gobox.log")

	rw, err := newRotatingWriter(path, 0, 0)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	if rw.maxBytes != defaultMaxLogBytes {
		t.Errorf("maxBytes = %d, 期望默认 %d", rw.maxBytes, defaultMaxLogBytes)
	}
	if rw.maxBackups != defaultMaxLogBackups {
		t.Errorf("maxBackups = %d, 期望默认 %d", rw.maxBackups, defaultMaxLogBackups)
	}
}

// TestRotationReopensAfterRotate 守护轮转后仍能继续写入（新句柄有效且从 0 计长）。
func TestRotationReopensAfterRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gobox.log")

	rw, err := newRotatingWriter(path, 64, 2)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer rw.Close()

	big := strings.Repeat("z", 100)
	if _, err := rw.Write([]byte(big)); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	// 第二次写入必然触发轮转。
	if _, err := rw.Write([]byte("after\n")); err != nil {
		t.Fatalf("轮转后写入: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取主文件: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("轮转后主文件应只含新记录, 实际 %q", data)
	}
	if rw.size != int64(len("after\n")) {
		t.Fatalf("轮转后 size = %d, 期望 %d", rw.size, len("after\n"))
	}
}

// TestRotationCloseIsIdempotent 守护 Close 可重复调用。
func TestRotationCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	rw, err := newRotatingWriter(filepath.Join(dir, "gobox.log"), 0, 0)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("首次 Close: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("重复 Close 不应报错: %v", err)
	}
	// 关闭后写入必须报错而不是静默丢弃。
	if _, err := rw.Write([]byte("x")); err == nil {
		t.Fatal("关闭后写入应返回错误")
	}
}

// itoaTest renders a small non-negative int without pulling strconv into the test.
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
