package clipboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersistHistoryOptionDefaultsOff(t *testing.T) {
	f := NewFeature().(*Feature)
	for _, option := range f.Options() {
		if option.Key == optPersistHistory {
			if option.Default != false {
				t.Fatalf("持久化历史默认值 = %v，隐私默认应关闭", option.Default)
			}
			return
		}
	}
	t.Fatal("Options() 未声明 persist_history")
}

func waitForHistoryStore(t *testing.T, path string, wantEntries int) historySnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var saved historySnapshot
			if json.Unmarshal(data, &saved) == nil && len(saved.Entries) == wantEntries {
				return saved
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待历史自动保存超时: %s", path)
	return historySnapshot{}
}

func TestPersistHistoryRestoresEntriesAndCache(t *testing.T) {
	dir := t.TempDir()
	values := map[string]any{"persist_history": true, "retention_days": 30}
	first, _ := newIngestFeature(t, values)
	first.ctx.DataDir = dir
	if err := first.Init(first.ctx); err != nil {
		t.Fatalf("初始化首个模块失败: %v", err)
	}

	text := first.hist.AddText("跨重启保留的文本")
	imagePath, imageSize, err := first.writePNGCache(first.ctx.Ctx, []byte("png-data"))
	if err != nil {
		t.Fatalf("写入图片缓存失败: %v", err)
	}
	image := first.hist.AddImageFile(imagePath, imageSize)
	waitForHistoryStore(t, filepath.Join(dir, historyStoreName), 2)

	second, _ := newIngestFeature(t, values)
	second.ctx.DataDir = dir
	if err := second.Init(second.ctx); err != nil {
		t.Fatalf("重启后初始化失败: %v", err)
	}
	entries := second.hist.All()
	if len(entries) != 2 {
		t.Fatalf("恢复条数 = %d，期望 2: %#v", len(entries), entries)
	}
	if entries[0].ID != text.ID || entries[0].Text != text.Text {
		t.Errorf("文本条目恢复不符: %#v", entries[0])
	}
	if entries[1].ID != image.ID || entries[1].Path != imagePath || entries[1].Size != imageSize {
		t.Errorf("图片条目恢复不符: %#v", entries[1])
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Errorf("恢复时图片缓存被清理: %v", err)
	}
	if got := second.hist.AddText("新条目").ID; got <= image.ID {
		t.Errorf("恢复后 ID 未继续递增: got=%d last=%d", got, image.ID)
	}
	waitForHistoryStore(t, filepath.Join(dir, historyStoreName), 3)

	store := filepath.Join(dir, historyStoreName)
	info, err := os.Stat(store)
	if err != nil {
		t.Fatalf("历史文件不存在: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("历史文件权限过宽: %o", info.Mode().Perm())
	}
}

func TestPersistHistoryOptOutRemovesStoredCopy(t *testing.T) {
	dir := t.TempDir()
	values := map[string]any{"persist_history": true}
	f, _ := newIngestFeature(t, values)
	f.ctx.DataDir = dir
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	f.hist.AddText("敏感文本")
	if err := f.persistHistory(); err != nil {
		t.Fatalf("保存历史失败: %v", err)
	}

	f.ctx.Config.(*ingestStubConfig).set(optPersistHistory, false)
	if err := f.ApplyOption(optPersistHistory, false); err != nil {
		t.Fatalf("关闭持久化失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, historyStoreName)); !os.IsNotExist(err) {
		t.Fatalf("关闭持久化后历史文件仍存在: %v", err)
	}
}

func TestPersistHistoryPrunesExpiredAndKeepsPinned(t *testing.T) {
	dir := t.TempDir()
	values := map[string]any{"persist_history": true, "retention_days": 30}
	first, _ := newIngestFeature(t, values)
	first.ctx.DataDir = dir
	if err := first.Init(first.ctx); err != nil {
		t.Fatalf("初始化首个模块失败: %v", err)
	}
	old := first.hist.AddText("过期文本")
	old.Timestamp = time.Now().AddDate(0, 0, -31)
	first.hist.Update(old)
	pinned := first.hist.AddText("固定文本")
	pinned.Timestamp = time.Now().AddDate(0, 0, -31)
	pinned.Pinned = true
	first.hist.Update(pinned)
	waitForHistoryStore(t, filepath.Join(dir, historyStoreName), 2)

	second, _ := newIngestFeature(t, values)
	second.ctx.DataDir = dir
	if err := second.Init(second.ctx); err != nil {
		t.Fatalf("重启后初始化失败: %v", err)
	}
	entries := second.hist.All()
	if len(entries) != 1 || entries[0].ID != pinned.ID || !entries[0].Pinned {
		t.Fatalf("保留期清理结果不符，期望仅保留置顶旧条目: %#v", entries)
	}
}

func TestWritePNGCacheUsesUniqueNames(t *testing.T) {
	f, _ := newIngestFeature(t, nil)
	paths := map[string]bool{}
	for i := 0; i < 5; i++ {
		path, _, err := f.writePNGCache(f.ctx.Ctx, []byte("same image"))
		if err != nil {
			t.Fatalf("第 %d 次写缓存失败: %v", i, err)
		}
		if paths[path] {
			t.Fatalf("缓存路径重复: %s", path)
		}
		paths[path] = true
	}
}

func TestRestoreHistoryRejectsImagePathOutsideCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, historyStoreName)
	saved := historySnapshot{
		Version: 1,
		Entries: []Entry{{ID: 1, Kind: KindImage, Path: filepath.Join(dir, "important.txt"), Size: 1}},
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = dir
	if err := f.restoreHistory(); err == nil {
		t.Fatal("不在图片缓存目录/命名规则内的路径应被拒绝")
	}
	if got := len(f.hist.All()); got != 0 {
		t.Fatalf("拒绝不可信历史后不应导入条目，实际 %d 条", got)
	}
}

func TestPersistHistoryCoalescesRapidWrites(t *testing.T) {
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = t.TempDir()
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	var writes atomic.Int32
	f.writeHistory = func(string, []byte) error {
		writes.Add(1)
		return nil
	}

	for i := 0; i < 6; i++ {
		f.requestPersist()
	}

	deadline := time.Now().Add(2 * time.Second)
	for writes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if writes.Load() == 0 {
		t.Fatal("防抖窗口结束后仍未写入")
	}
	// 再等一个完整窗口，确保没有残留的重复定时器补写。
	time.Sleep(historySaveDelay + 100*time.Millisecond)
	if got := writes.Load(); got != 1 {
		t.Fatalf("连续变更写盘次数 = %d，期望合并为 1 次", got)
	}
}

func TestStopFlushesScheduledHistoryWrite(t *testing.T) {
	dir := t.TempDir()
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = dir
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	f.hist.AddText("停止前待写条目")
	f.requestPersist()

	if err := f.Stop(); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, historyStoreName))
	if err != nil {
		t.Fatalf("Stop 未落盘待写历史: %v", err)
	}
	var saved historySnapshot
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Stop 写入的历史文件不可解析: %v", err)
	}
	if len(saved.Entries) != 1 || saved.Entries[0].Text != "停止前待写条目" {
		t.Fatalf("Stop 落盘内容不符: %#v", saved.Entries)
	}
}

func TestDisablePersistCancelsScheduledWrite(t *testing.T) {
	dir := t.TempDir()
	values := map[string]any{"persist_history": true}
	f, _ := newIngestFeature(t, values)
	f.ctx.DataDir = dir
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	f.hist.AddText("关闭前待写条目")
	f.requestPersist()

	f.ctx.Config.(*ingestStubConfig).set(optPersistHistory, false)
	if err := f.ApplyOption(optPersistHistory, false); err != nil {
		t.Fatalf("关闭持久化失败: %v", err)
	}
	time.Sleep(historySaveDelay + 200*time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, historyStoreName)); !os.IsNotExist(err) {
		t.Fatalf("关闭持久化后待写定时器仍重建了历史文件: %v", err)
	}
}

func TestWriteHistoryFileFallsBackWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, historyStoreName)
	failingRename := func(string, string) error {
		return errors.New("target locked")
	}

	want := []byte(`{"version":1,"next_id":0,"entries":[]}`)
	if err := writeHistoryFileWith(path, want, failingRename); err != nil {
		t.Fatalf("rename 失败时应回退直写: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回退直写后文件不存在: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("回退直写内容不符: %q", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".clipboard-history-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("回退后残留临时文件: %v", leftovers)
	}
}

func TestEntryJSONFieldNamesAreStable(t *testing.T) {
	data, err := json.Marshal(Entry{
		ID:        7,
		Kind:      KindText,
		Text:      "hello",
		Path:      "/tmp/cache.png",
		Size:      5,
		Timestamp: time.Unix(0, 0).UTC(),
		Pinned:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("条目 JSON 不可解析: %v", err)
	}
	want := []string{"id", "kind", "text", "path", "size", "timestamp", "pinned"}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("条目 JSON 缺少稳定字段 %q: %s", key, data)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("条目 JSON 字段数 = %d，期望 %d: %s", len(fields), len(want), data)
	}
}

func TestRestoreHistoryRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, historyStoreName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = dir
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("损坏的历史文件不应阻断 Init: %v", err)
	}
	if got := len(f.hist.All()); got != 0 {
		t.Fatalf("损坏文件不应导入任何条目，实际 %d 条", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("恢复失败时应保留原文件供排查: %v", err)
	}
}

func TestRestoreHistoryRejectsUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, historyStoreName)
	data, err := json.Marshal(historySnapshot{Version: 99})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = dir
	if err := f.restoreHistory(); err == nil {
		t.Fatal("未知版本的历史文件应被拒绝")
	}
	if got := len(f.hist.All()); got != 0 {
		t.Fatalf("拒绝未知版本后不应导入条目，实际 %d 条", got)
	}
}

func TestRestoreHistoryDropsMissingImageCache(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, cachePrefix+"missing"+cacheExt)
	saved := historySnapshot{
		Version: 1,
		NextID:  2,
		Entries: []Entry{
			{ID: 1, Kind: KindText, Text: "保留", Timestamp: time.Now()},
			{ID: 2, Kind: KindImage, Path: missing, Size: 10, Timestamp: time.Now()},
		},
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, historyStoreName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := newIngestFeature(t, map[string]any{"persist_history": true})
	f.ctx.DataDir = dir
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	entries := f.hist.All()
	if len(entries) != 1 || entries[0].Text != "保留" {
		t.Fatalf("缓存缺失的图片条目应被跳过，实际: %#v", entries)
	}
	if got := f.hist.AddText("新条目").ID; got <= saved.NextID {
		t.Fatalf("恢复后 ID 未延续: got=%d next_id=%d", got, saved.NextID)
	}
}
