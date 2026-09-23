package taskbar

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// fakeConfig 是最小的 core.ModuleConfig 实现，用 map 承载 Get/Set，
// 让选项驱动的辅助函数无需加载真实配置文件即可被测试。
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

// testLogger 把模块日志丢进 io.Discard，避免污染测试输出。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestFeature 构造一个已接线 core.Context 的 *Feature。
func newTestFeature(t *testing.T, opts map[string]any) *Feature {
	t.Helper()
	f, ok := NewFeature().(*Feature)
	if !ok {
		t.Fatalf("NewFeature() 返回的不是 *Feature: %T", NewFeature())
	}
	f.ctx = &core.Context{
		Ctx:    t.Context(),
		Logger: testLogger(),
		Bus:    core.NewBus(),
		Config: &fakeConfig{opts: opts},
	}
	return f
}

// TestFeatureIdentity 守护模块身份：ID 必须与目录名一致，Name 非空。
func TestFeatureIdentity(t *testing.T) {
	f := &Feature{}
	if got, want := f.ID(), moduleID; got != want {
		t.Errorf("ID() = %q, 期望 %q", got, want)
	}
	if f.Name() == "" {
		t.Error("Name() 不应为空")
	}
}

// TestOptionsExposeEveryKey 守护面板表单与 feature.go 里声明的 opt* 常量同步：
// 每个常量都要出现在 Options() 里，且不能重复。
func TestOptionsExposeEveryKey(t *testing.T) {
	want := []string{
		optInterval, optShowDown, optShowUp, optShowCPU, optShowMem, optShowDisk,
		optShowUptime, optAlign, optOffsetX, optMarginTop, optMarginV, optLayout,
		optNumAlign, optSpeedUnit, optUnitSpace, optFontFamily, optFontSize,
		optFGColor, optBGMode, optBGColor, optFollowTheme, optSeparator, optRender,
		optAvoidWidgets, optMultiMonitor,
	}

	f := &Feature{}
	seen := map[string]bool{}
	for _, o := range f.Options() {
		if o.Key == "" {
			t.Error("存在 Key 为空的配置项")
		}
		if seen[o.Key] {
			t.Errorf("配置项重复暴露: %s", o.Key)
		}
		seen[o.Key] = true
		if o.Label == "" {
			t.Errorf("配置项 %s 缺少 Label，面板无法渲染", o.Key)
		}
	}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("模块未暴露配置项: %s（feature.go 声明了 opt 常量但 Options() 里没有）", k)
		}
	}
}

// TestOptionsSelectHaveChoicesAndValidDefaults 守护下拉类配置项：必须有选项
// 列表，且默认值落在选项里，否则面板会渲染出一个无法回显的表单。
func TestOptionsSelectHaveChoicesAndValidDefaults(t *testing.T) {
	for _, o := range (&Feature{}).Options() {
		if o.Kind != core.KindSelect {
			continue
		}
		if len(o.Choices) == 0 {
			t.Errorf("下拉配置项 %s 没有 Choices", o.Key)
			continue
		}
		found := false
		for _, ch := range o.Choices {
			if ch.Value == o.Default {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("下拉配置项 %s 的默认值 %v 不在 Choices 里", o.Key, o.Default)
		}
	}
}

// TestActionsExposeDeclaredIDs 守护面板按钮：Actions() 必须暴露已实现的
// action id。copy_stats 目前只定义了常量、尚未接线，这里显式断言它确实
// 没有出现在面板上，一旦将来补上实现，本用例会提醒同步更新。
func TestActionsExposeDeclaredIDs(t *testing.T) {
	ids := map[string]bool{}
	for _, a := range (&Feature{}).Actions() {
		if a.ID == "" {
			t.Error("存在 ID 为空的操作")
		}
		if a.Label == "" {
			t.Errorf("操作 %s 缺少 Label", a.ID)
		}
		ids[a.ID] = true
	}
	for _, want := range []string{actionToggle, actionReset} {
		if !ids[want] {
			t.Errorf("缺少操作: %s（feature.go 声明了但 Actions() 里没有）", want)
		}
	}
	if ids[actionCopy] {
		t.Errorf("操作 %s 已在 Actions() 中暴露，但它还没有 RunAction 分支，请补上实现", actionCopy)
	}
}

// TestToIntAcceptsConfigShapes 守护 toInt：YAML/JSON 配置里可能出现的数值
// 形态都要能被识别，非数值形态一律返回 false。
func TestToIntAcceptsConfigShapes(t *testing.T) {
	type tc struct {
		name string
		in   any
		want int
		ok   bool
	}
	cases := []tc{
		{"int", 42, 42, true},
		{"int 零值", 0, 0, true},
		{"int 负值", -7, -7, true},
		{"int32", int32(11), 11, true},
		{"int64", int64(3000000000), 3000000000, true},
		{"float32", float32(3.0), 3, true},
		{"float64 整数", float64(9), 9, true},
		{"float64 截断小数", 12.9, 12, true},
		{"string 数字", "42", 0, false},
		{"string 非数字", "abc", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
		{"uint", uint(5), 0, false},
		{"切片", []int{1}, 0, false},
	}
	for _, c := range cases {
		got, ok := toInt(c.in)
		if ok != c.ok {
			t.Errorf("toInt(%v) 的 ok = %v, 期望 %v（%s）", c.in, ok, c.ok, c.name)
			continue
		}
		if got != c.want {
			t.Errorf("toInt(%v) = %d, 期望 %d（%s）", c.in, got, c.want, c.name)
		}
	}
}

// TestRound1RoundsToSingleDecimal 守护 round1 的一位小数四舍五入，含
// 12.34->12.3、12.35->12.4 这类边界。
func TestRound1RoundsToSingleDecimal(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{1, 1},
		{12.34, 12.3},
		{12.35, 12.4},
		{12.36, 12.4},
		{12.349, 12.3},
		{99.95, 100.0},
		{0.05, 0.1},
		{123.456, 123.5},
		{100.0, 100.0},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, 期望 %v", c.in, got, c.want)
		}
	}
}

// TestPctFormatsWithoutDecimals 守护 pct 的整数百分比文本（四舍五入且带 %）。
func TestPctFormatsWithoutDecimals(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0%"},
		{12.34, "12%"},
		{12.49, "12%"},
		{12.5, "13%"},
		{99.5, "100%"},
		{100, "100%"},
		{0.4, "0%"},
	}
	for _, c := range cases {
		if got := pct(c.in); got != c.want {
			t.Errorf("pct(%v) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestIntervalClampsToSafeBounds 守护采样间隔的业务钳制：低于 200ms 拉到
// 200ms，高于 10s 压到 10s，非法值回退默认值 1000ms。
func TestIntervalClampsToSafeBounds(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want time.Duration
	}{
		{"默认值", nil, 1000 * time.Millisecond},
		{"低于下限", 50, minInterval},
		{"正好下限", 200, minInterval},
		{"区间内", 1500, 1500 * time.Millisecond},
		{"正好上限", 10000, maxInterval},
		{"高于上限", 60000, maxInterval},
		{"非法字符串", "fast", 1000 * time.Millisecond},
		{"float64 形态", float64(800), 800 * time.Millisecond},
	}
	for _, c := range cases {
		opts := map[string]any{}
		if c.in != nil {
			opts[optInterval] = c.in
		}
		f := newTestFeature(t, opts)
		if got := f.interval(); got != c.want {
			t.Errorf("interval() = %v, 期望 %v（%s，配置 %v）", got, c.want, c.name, c.in)
		}
	}
}

// TestOffsetXReadsConfiguredValue 守护水平偏移：读配置值，缺失时回退默认 8。
func TestOffsetXReadsConfiguredValue(t *testing.T) {
	if got, want := newTestFeature(t, nil).offsetX(), defaultOffsetX; got != want {
		t.Errorf("offsetX() = %d, 期望默认值 %d", got, want)
	}
	f := newTestFeature(t, map[string]any{optOffsetX: -16})
	if got, want := f.offsetX(), -16; got != want {
		t.Errorf("offsetX() = %d, 期望 %d", got, want)
	}
}

// TestRateFormatsBySpeedUnit 守护速率单位换算：B / b / fixedKB 三种模式，
// 以及数值与单位之间的空格开关。
func TestRateFormatsBySpeedUnit(t *testing.T) {
	cases := []struct {
		name  string
		unit  string
		space bool
		bps   float64
		want  string
	}{
		{"默认字节带空格", "", true, 512, "512 B/s"},
		{"字节 K 档", "B", true, 2048, "2K B/s"},
		{"比特换算（×8）带空格", "b", true, 512, "4K b/s"},
		{"比特不带空格", "b", false, 512, "4Kb/s"},
		{"固定 KB 换算（2048B/s=2KB/s）", "fixedKB", true, 2048, "2 KB/s"},
		{"固定 KB 换算（2MB/s=2048KB/s）", "fixedKB", true, 2 * 1024 * 1024, "2K KB/s"},
		{"固定 KB 不足 1KB 显示 0", "fixedKB", true, 100, "0 KB/s"},
		{"负速率归零", "B", true, -100, "0 B/s"},
	}
	for _, c := range cases {
		opts := map[string]any{optUnitSpace: c.space}
		if c.unit != "" {
			opts[optSpeedUnit] = c.unit
		}
		f := newTestFeature(t, opts)
		if got := f.rate(c.bps); got != c.want {
			t.Errorf("rate(%v) = %q, 期望 %q（%s）", c.bps, got, c.want, c.name)
		}
	}
}

// TestUnitSepHonoursSpaceOption 守护 unitSep：unit_space 打开时是空格，
// 关闭时是空串（面板上直接贴着单位显示）。
func TestUnitSepHonoursSpaceOption(t *testing.T) {
	if got, want := newTestFeature(t, nil).unitSep(), " "; got != want {
		t.Errorf("unitSep() = %q, 期望 %q（默认应带空格）", got, want)
	}
	if got, want := newTestFeature(t, map[string]any{optUnitSpace: true}).unitSep(), " "; got != want {
		t.Errorf("unitSep() = %q, 期望 %q（unit_space=true）", got, want)
	}
	if got, want := newTestFeature(t, map[string]any{optUnitSpace: false}).unitSep(), ""; got != want {
		t.Errorf("unitSep() = %q, 期望 %q（unit_space=false）", got, want)
	}
}

// TestStateReportsPanelFields 守护面板读取的 State 字段：running / interval_ms
// 等键必须存在，且类型和取值正确。
func TestStateReportsPanelFields(t *testing.T) {
	f := newTestFeature(t, map[string]any{optInterval: 500})
	if err := f.Init(f.ctx); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	f.mu.Lock()
	f.running = true
	f.last = Stats{CPU: 12.34, RAM: 55.5, Disk: 70.25, NetUp: 2048, NetDn: 512, Uptime: 90 * time.Second}
	f.mu.Unlock()

	st := f.State()

	if got, ok := st["running"].(bool); !ok || !got {
		t.Errorf("State[\"running\"] = %v, 期望 bool true", st["running"])
	}
	if got, ok := st["interval_ms"].(int); !ok || got != 500 {
		t.Errorf("State[\"interval_ms\"] = %v, 期望 int 500", st["interval_ms"])
	}
	if got, ok := st["visible"].(bool); !ok || !got {
		t.Errorf("State[\"visible\"] = %v, 期望 bool true（running 且未隐藏）", st["visible"])
	}
	if _, ok := st["native"].(bool); !ok {
		t.Errorf("State[\"native\"] = %v, 期望 bool", st["native"])
	}
	if got, ok := st["cpu_percent"].(float64); !ok || got != 12.3 {
		t.Errorf("State[\"cpu_percent\"] = %v, 期望 float64 12.3", st["cpu_percent"])
	}
	if got, ok := st["mem_percent"].(float64); !ok || got != 55.5 {
		t.Errorf("State[\"mem_percent\"] = %v, 期望 float64 55.5", st["mem_percent"])
	}
	if got, ok := st["disk_percent"].(float64); !ok || got != 70.3 {
		t.Errorf("State[\"disk_percent\"] = %v, 期望 float64 70.3", st["disk_percent"])
	}
	if got, ok := st["upload_rate"].(string); !ok || got != "2K B/s" {
		t.Errorf("State[\"upload_rate\"] = %v, 期望 \"2K B/s\"", st["upload_rate"])
	}
	if got, ok := st["download_rate"].(string); !ok || got != "512 B/s" {
		t.Errorf("State[\"download_rate\"] = %v, 期望 \"512 B/s\"", st["download_rate"])
	}
	if got, ok := st["uptime"].(string); !ok || got != "1m30s" {
		t.Errorf("State[\"uptime\"] = %v, 期望 \"1m30s\"", st["uptime"])
	}
	if _, ok := st["window"].(string); !ok {
		t.Errorf("State[\"window\"] = %v, 期望 string", st["window"])
	}
}

// TestStateReflectsStoppedAndHidden 守护状态开关：未运行时 running/visible
// 都必须为 false，避免面板在模块停止后仍显示"运行中"。
func TestStateReflectsStoppedAndHidden(t *testing.T) {
	f := newTestFeature(t, nil)
	st := f.State()
	if got, ok := st["running"].(bool); !ok || got {
		t.Errorf("未启动时 State[\"running\"] = %v, 期望 false", st["running"])
	}
	if got, ok := st["visible"].(bool); !ok || got {
		t.Errorf("未启动时 State[\"visible\"] = %v, 期望 false", st["visible"])
	}

	// 运行中但隐藏：running 仍为 true，visible 必须为 false。
	f.mu.Lock()
	f.running = true
	f.hidden = true
	f.mu.Unlock()
	st = f.State()
	if got, ok := st["running"].(bool); !ok || !got {
		t.Errorf("运行中 State[\"running\"] = %v, 期望 true", st["running"])
	}
	if got, ok := st["visible"].(bool); !ok || got {
		t.Errorf("隐藏时 State[\"visible\"] = %v, 期望 false", st["visible"])
	}
}
