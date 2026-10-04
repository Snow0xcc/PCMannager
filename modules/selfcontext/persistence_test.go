package selfcontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// historyPath is the on-disk location of the persisted history file.
func historyPath(ctx *core.Context) string {
	return filepath.Join(ctx.DataDir, historyFileName)
}

// readHistoryFile decodes the persisted history file for assertions.
func readHistoryFile(t *testing.T, ctx *core.Context) []Entry {
	t.Helper()
	raw, err := os.ReadFile(historyPath(ctx))
	if err != nil {
		t.Fatalf("读取历史文件失败: %v", err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("历史文件不是合法 JSON: %v\n内容: %s", err, raw)
	}
	return entries
}

// TestPersistenceRoundtrip proves a saved history comes back identical through
// a fresh Init, on every platform (no ActiveWindow involvement).
func TestPersistenceRoundtrip(t *testing.T) {
	f1, ctx := newTestFeature(t, nil)
	// Recent timestamps: load() prunes by retention_days, so ancient samples
	// would be legitimately dropped before the roundtrip comparison.
	ts := time.Now().Add(-time.Minute).Truncate(time.Second)
	appendEntry(f1, "编辑器", "code.exe", ts)
	appendEntry(f1, "终端", "", ts.Add(time.Minute))
	f1.save()

	f2 := &Feature{ctx: ctx}
	if err := f2.Init(ctx); err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}

	got := f2.Snapshot()
	if len(got) != 2 {
		t.Fatalf("重启后 %d 条, 期望 2", len(got))
	}
	if got[0].Title != "编辑器" || got[0].Process != "code.exe" || !got[0].Timestamp.Equal(ts) {
		t.Errorf("第 1 条 = %+v, 期望 {编辑器 code.exe %v}", got[0], ts)
	}
	if got[1].Title != "终端" || got[1].Process != "" {
		t.Errorf("第 2 条 = %+v, 期望 {终端 \"\"}", got[1])
	}
}

// TestLoadPrunesByRetention applies retention_days to the loaded file and
// rewrites it, so an expired entry never comes back.
func TestLoadPrunesByRetention(t *testing.T) {
	f, ctx := newTestFeature(t, map[string]any{optRetentionDays: 7})
	now := time.Now()
	seed := []Entry{
		{Title: "过期窗口", Process: "old.exe", Timestamp: now.AddDate(0, 0, -10)},
		{Title: "新鲜窗口", Process: "new.exe", Timestamp: now},
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("构造种子数据失败: %v", err)
	}
	if err := os.WriteFile(historyPath(ctx), data, 0o600); err != nil {
		t.Fatalf("写入种子数据失败: %v", err)
	}

	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	got := f.Snapshot()
	if len(got) != 1 || got[0].Title != "新鲜窗口" {
		t.Fatalf("加载后应只剩新鲜条目, 实际 %+v", got)
	}
	onDisk := readHistoryFile(t, ctx)
	if len(onDisk) != 1 || onDisk[0].Title != "新鲜窗口" {
		t.Fatalf("修剪结果应落盘, 实际 %+v", onDisk)
	}
}

// TestCorruptHistoryStartsFresh proves a damaged file is dropped with a fresh
// start instead of crashing Init.
func TestCorruptHistoryStartsFresh(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	if err := os.WriteFile(historyPath(ctx), []byte("{\"title\": 不是JSON"), 0o600); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}

	if err := f.Init(ctx); err != nil {
		t.Fatalf("损坏文件不应让 Init 失败: %v", err)
	}
	if got := len(f.Snapshot()); got != 0 {
		t.Fatalf("损坏文件应被丢弃, 实际 %d 条", got)
	}

	// The module must recover: a new sample repopulates a valid file.
	appendEntry(f, "恢复后", "ok.exe", time.Now())
	f.save()
	onDisk := readHistoryFile(t, ctx)
	if len(onDisk) != 1 || onDisk[0].Title != "恢复后" {
		t.Fatalf("损坏后应能重新落盘, 实际 %+v", onDisk)
	}
}

// TestClearPersistsEmpty keeps the file consistent with the in-memory Clear.
func TestClearPersistsEmpty(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	appendEntry(f, "A", "a.exe", time.Now())
	f.save()

	f.Clear()

	onDisk := readHistoryFile(t, ctx)
	if len(onDisk) != 0 {
		t.Fatalf("清空后文件应变为空, 实际 %+v", onDisk)
	}
}

// TestRecordPersistsCapturedSample drives the real sampling path through the
// injected window probe, proving captured entries reach the disk file without
// any platform window involvement.
func TestRecordPersistsCapturedSample(t *testing.T) {
	f, ctx := newTestFeature(t, map[string]any{optPauseOnLock: false})
	f.activeWindow = func() (string, string, error) { return "注入标题", "app.exe", nil }

	f.record()

	onDisk := readHistoryFile(t, ctx)
	if len(onDisk) != 1 {
		t.Fatalf("采样后文件应有 1 条, 实际 %+v", onDisk)
	}
	if onDisk[0].Title != "注入标题" || onDisk[0].Process != "app.exe" {
		t.Errorf("落盘条目 = %+v, 期望 {注入标题 app.exe}", onDisk[0])
	}
}

// TestHistoryFileStaysBoundedAtMaxEntries mirrors the in-memory cap: a loaded
// history longer than maxEntries is trimmed to the newest maxEntries entries.
func TestHistoryFileStaysBoundedAtMaxEntries(t *testing.T) {
	f, ctx := newTestFeature(t, nil)
	now := time.Now()
	seed := make([]Entry, 0, maxEntries+10)
	for i := 0; i < maxEntries+10; i++ {
		seed = append(seed, Entry{Title: "w", Process: "p", Timestamp: now.Add(time.Duration(i) * time.Second)})
	}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("构造种子数据失败: %v", err)
	}
	if err := os.WriteFile(historyPath(ctx), data, 0o600); err != nil {
		t.Fatalf("写入种子数据失败: %v", err)
	}

	if err := f.Init(ctx); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if got := len(f.Snapshot()); got != maxEntries {
		t.Fatalf("加载后 %d 条, 应封顶于 %d", got, maxEntries)
	}
}
