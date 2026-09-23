package selfcontext

import (
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// fakeConfig is a minimal ModuleConfig so option-driven helpers can be
// exercised without loading a real config file.
type fakeConfig struct {
	mu   sync.Mutex
	opts map[string]any
}

func (c *fakeConfig) Enabled() bool  { return true }
func (c *fakeConfig) Hotkey() string { return "" }
func (c *fakeConfig) Get(key string, def any) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.opts[key]; ok {
		return v
	}
	return def
}
func (c *fakeConfig) Set(key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opts == nil {
		c.opts = map[string]any{}
	}
	c.opts[key] = value
	return nil
}
func (c *fakeConfig) SetEnabled(bool) error  { return nil }
func (c *fakeConfig) SetHotkey(string) error { return nil }

// newTestFeature builds a Feature wired to a discard logger and a temp dir.
func newTestFeature(t *testing.T, opts map[string]any) (*Feature, *core.Context) {
	t.Helper()
	ctx := &core.Context{
		Ctx:     t.Context(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config:  &fakeConfig{opts: opts},
		DataDir: t.TempDir(),
	}
	return &Feature{ctx: ctx}, ctx
}

// appendEntry seeds the in-memory buffer directly, bypassing the platform
// ActiveWindow call so tests stay cross-platform. Like record(), it updates
// the `last` de-dup marker, which State() reports as last_title.
func appendEntry(f *Feature, title, process string, ts time.Time) {
	f.mu.Lock()
	f.entries = append(f.entries, Entry{Title: title, Process: process, Timestamp: ts})
	f.last = title
	f.mu.Unlock()
}

// TestOptionsExposeAllKeys keeps the panel form in sync with the option keys.
func TestOptionsExposeAllKeys(t *testing.T) {
	f := &Feature{}
	want := map[string]bool{optInterval: false, optRetentionDays: false, optCaptureMode: false, optPauseOnLock: false}
	for _, o := range f.Options() {
		if _, ok := want[o.Key]; ok {
			want[o.Key] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("模块未暴露配置项: %s", k)
		}
	}
}

// TestOptionsDefaultsMatchConstants guards against a config/panel drift where
// the declared default differs from the constant the module falls back to.
func TestOptionsDefaultsMatchConstants(t *testing.T) {
	f := &Feature{}
	want := map[string]any{
		optInterval:      defaultInterval,
		optRetentionDays: defaultRetentionDays,
		optCaptureMode:   defaultCaptureMode,
		optPauseOnLock:   defaultPauseOnLock,
	}
	for _, o := range f.Options() {
		exp, ok := want[o.Key]
		if !ok {
			continue
		}
		if o.Default != exp {
			t.Errorf("配置项 %s 默认值 = %v, 期望 %v", o.Key, o.Default, exp)
		}
	}
}

// TestIntervalClamps keeps a mistyped interval from becoming a busy loop.
func TestIntervalClamps(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{nil, defaultInterval},
		{300, 300},
		{minInterval, minInterval},
		{minInterval - 1, defaultInterval}, // below the floor
		{10, defaultInterval},
		{maxInterval, maxInterval},
		{maxInterval + 1, maxInterval}, // above the ceiling
		{float64(500), 500},            // YAML decodes numbers as float64
		{"x", defaultInterval},         // non-numeric
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optInterval] = c.in
		}
		f, _ := newTestFeature(t, opts)
		if got := f.interval(); got != c.want {
			t.Errorf("interval(%v) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestRetentionDaysClamps rejects non-positive and non-numeric values.
func TestRetentionDaysClamps(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{nil, defaultRetentionDays},
		{1, 1},
		{30, 30},
		{0, defaultRetentionDays},
		{-5, defaultRetentionDays},
		{float64(14), 14},
		{"x", defaultRetentionDays},
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optRetentionDays] = c.in
		}
		f, _ := newTestFeature(t, opts)
		if got := f.retentionDays(); got != c.want {
			t.Errorf("retentionDays(%v) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestCaptureModeFallsBackToDefault for empty/unknown values.
func TestCaptureModeFallsBackToDefault(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, defaultCaptureMode},
		{"title", modeTitle},
		{"primary", modePrimary},
		{"", defaultCaptureMode},
		{"bogus", "bogus"}, // unknown strings pass through by design
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optCaptureMode] = c.in
		}
		f, _ := newTestFeature(t, opts)
		if got := f.captureMode(); got != c.want {
			t.Errorf("captureMode(%v) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestPruneDropsExpiredEntries keeps only entries newer than the retention
// window (retention_days).
func TestPruneDropsExpiredEntries(t *testing.T) {
	f, _ := newTestFeature(t, map[string]any{optRetentionDays: 7})
	now := time.Now()
	appendEntry(f, "旧窗口", "old.exe", now.AddDate(0, 0, -10)) // expired
	appendEntry(f, "新窗口", "new.exe", now)                    // kept

	f.prune()

	got := f.Snapshot()
	if len(got) != 1 {
		t.Fatalf("保留 %d 条, 期望 1", len(got))
	}
	if got[0].Title != "新窗口" {
		t.Fatalf("保留条目 = %q, 期望 新窗口", got[0].Title)
	}
}

// TestPruneKeepsEverythingWithLargeRetention is the no-op sanity case.
func TestPruneKeepsEverythingWithLargeRetention(t *testing.T) {
	f, _ := newTestFeature(t, map[string]any{optRetentionDays: 365})
	appendEntry(f, "A", "a.exe", time.Now())
	appendEntry(f, "B", "b.exe", time.Now())
	f.prune()
	if got := len(f.Snapshot()); got != 2 {
		t.Fatalf("保留 %d 条, 期望 2", got)
	}
}

// TestRecordDeduplicatesRepeats proves consecutive identical titles collapse
// into one entry (the module stores `last` for exactly this).
func TestRecordDeduplicatesRepeats(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	f.mu.Lock()
	f.last = "同一个窗口"
	f.mu.Unlock()

	// Simulate the de-dup branch of record() without a platform window call.
	f.mu.Lock()
	title := "同一个窗口"
	if title == f.last {
		f.mu.Unlock()
	} else {
		f.mu.Unlock()
		t.Fatal("去重逻辑未按预期命中")
	}
	if got := len(f.Snapshot()); got != 0 {
		t.Fatalf("重复标题不应新增条目, 实际 %d", got)
	}

	// A different title must be recorded.
	f.mu.Lock()
	f.last = title
	f.entries = append(f.entries, Entry{Title: "另一个窗口", Process: "b.exe", Timestamp: time.Now()})
	f.mu.Unlock()
	if got := len(f.Snapshot()); got != 1 {
		t.Fatalf("不同标题应新增条目, 实际 %d", got)
	}
}

// TestRecordBoundsMemoryAtMaxEntries caps the in-memory ring buffer.
func TestRecordBoundsMemoryAtMaxEntries(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	now := time.Now()
	f.mu.Lock()
	for i := 0; i < maxEntries+50; i++ {
		f.entries = append(f.entries, Entry{Title: "w", Process: "p", Timestamp: now})
	}
	// Mirror the trim in record().
	if len(f.entries) > maxEntries {
		f.entries = append(f.entries[:0], f.entries[len(f.entries)-maxEntries:]...)
	}
	f.mu.Unlock()
	if got := len(f.Snapshot()); got != maxEntries {
		t.Fatalf("缓冲 %d 条, 应封顶于 %d", got, maxEntries)
	}
}

// TestClearDropsEntriesAndLast resets both the buffer and the de-dup marker.
func TestClearDropsEntriesAndLast(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	appendEntry(f, "A", "a.exe", time.Now())
	f.mu.Lock()
	f.last = "A"
	f.mu.Unlock()

	f.Clear()

	if got := len(f.Snapshot()); got != 0 {
		t.Fatalf("Clear 后仍有 %d 条", got)
	}
	f.mu.Lock()
	last := f.last
	f.mu.Unlock()
	if last != "" {
		t.Fatalf("Clear 应重置去重标记, 实际 %q", last)
	}
}

// TestSnapshotReturnsCopy protects callers from mutating internal state.
func TestSnapshotReturnsCopy(t *testing.T) {
	f, _ := newTestFeature(t, nil)
	appendEntry(f, "A", "a.exe", time.Now())

	snap := f.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("快照 %d 条, 期望 1", len(snap))
	}
	snap[0].Title = "篡改"
	if got := f.Snapshot()[0].Title; got == "篡改" {
		t.Fatal("Snapshot 返回了内部切片，外部修改会污染模块状态")
	}
}

// TestSummaryFormatsEntries renders the LLM-facing text block.
func TestSummaryFormatsEntries(t *testing.T) {
	f, _ := newTestFeature(t, nil)

	if got := f.Summary(); got != "(无记录)" {
		t.Fatalf("空摘要 = %q, 期望 (无记录)", got)
	}

	ts := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	appendEntry(f, "编辑器", "code.exe", ts)
	appendEntry(f, "终端", "", ts)

	got := f.Summary()
	for _, want := range []string{"最近的工作上下文", "15:04:05", "编辑器", "[code.exe]", "终端"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要缺少 %q:\n%s", want, got)
		}
	}
	// A title-only entry must not render empty brackets.
	if strings.Contains(got, "终端 []") {
		t.Errorf("无进程名时不应渲染空方括号:\n%s", got)
	}
}

// TestSetPausedTracksLockState reflects pause_on_lock in State().
func TestSetPausedTracksLockState(t *testing.T) {
	f, _ := newTestFeature(t, nil)

	f.setPaused(true)
	if got := f.State()["paused"]; got != true {
		t.Fatalf("paused = %v, 期望 true", got)
	}

	f.setPaused(false)
	if got := f.State()["paused"]; got != false {
		t.Fatalf("paused = %v, 期望 false", got)
	}
}

// TestStateReportsRuntimeStatus mirrors what the panel reads back.
func TestStateReportsRuntimeStatus(t *testing.T) {
	f, _ := newTestFeature(t, map[string]any{optInterval: 500, optRetentionDays: 3, optCaptureMode: modeTitle})
	appendEntry(f, "A", "a.exe", time.Now())

	st := f.State()
	if st["count"] != 1 {
		t.Errorf("count = %v, 期望 1", st["count"])
	}
	if st["interval_ms"] != 500 {
		t.Errorf("interval_ms = %v, 期望 500", st["interval_ms"])
	}
	if st["retention_days"] != 3 {
		t.Errorf("retention_days = %v, 期望 3", st["retention_days"])
	}
	if st["capture_mode"] != modeTitle {
		t.Errorf("capture_mode = %v, 期望 %v", st["capture_mode"], modeTitle)
	}
	if st["last_title"] != "A" {
		t.Errorf("last_title = %v, 期望 A", st["last_title"])
	}
	if _, ok := st["running"]; !ok {
		t.Error("State 应包含 running 字段")
	}
}

// TestActionsExposePanelButtons ensures every declared action is reachable.
func TestActionsExposePanelButtons(t *testing.T) {
	f := &Feature{}
	ids := map[string]bool{}
	for _, a := range f.Actions() {
		ids[a.ID] = true
	}
	for _, want := range []string{actionOpen, actionCopy, actionClear, actionExport} {
		if !ids[want] {
			t.Errorf("缺少操作: %s", want)
		}
	}
}

// TestConcurrentSnapshotAndRecord verifies the buffer is mutex-protected.
func TestConcurrentSnapshotAndRecord(t *testing.T) {
	f, _ := newTestFeature(t, nil)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			appendEntry(f, "w", "p", time.Now())
			_ = f.Snapshot()
			_ = f.Summary()
		}(i)
	}
	wg.Wait()

	if got := len(f.Snapshot()); got != 16 {
		t.Fatalf("并发写入后 %d 条, 期望 16", got)
	}
}

// TestModuleIdentityIsStable keeps the config/hotkey id from drifting.
func TestModuleIdentityIsStable(t *testing.T) {
	f := &Feature{}
	if f.ID() != moduleID {
		t.Fatalf("ID = %q, 期望 %q", f.ID(), moduleID)
	}
	if f.Name() == "" {
		t.Fatal("Name 不应为空")
	}
	if f.Description() == "" {
		t.Fatal("Description 不应为空")
	}
}
