// Package taskbar renders a TrafficMonitor-style status strip inside the
// Windows taskbar: live CPU / memory / network / disk readouts that sit next to
// the clock without stealing space from real taskbar buttons.
//
// Off Windows the module still samples and publishes the same metrics; only the
// native widget is unavailable, so the panel shows the numbers instead.
package taskbar

import (
	"strconv"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// moduleID is the stable identifier used by the registry, the configuration
// file and hotkey bindings; it must match the directory name.
const moduleID = "taskbar"

// Option keys. They mirror the "taskbar" section of internal/config.Default().
const (
	optInterval     = "interval"
	optShowDown     = "show_download"
	optShowUp       = "show_upload"
	optShowCPU      = "show_cpu"
	optShowMem      = "show_mem"
	optShowDisk     = "show_disk"
	optShowUptime   = "show_uptime"
	optAlign        = "align"
	optOffsetX      = "offset_x"
	optMarginTop    = "margin_top"
	optMarginV      = "margin_v"
	optLayout       = "layout"
	optNumAlign     = "num_align"
	optSpeedUnit    = "speed_unit"
	optUnitSpace    = "unit_space"
	optFontFamily   = "font_family"
	optFontSize     = "font_size"
	optFGColor      = "fg_color"
	optBGMode       = "bg_mode"
	optBGColor      = "bg_color"
	optFollowTheme  = "follow_theme"
	optSeparator    = "separator"
	optRender       = "render"
	optAvoidWidgets = "avoid_widgets"
	optMultiMonitor = "multi_monitor"
)

// Option defaults, kept in sync with internal/config.Default().
const (
	defaultInterval     = 1000
	defaultShowDown     = true
	defaultShowUp       = true
	defaultShowCPU      = false
	defaultShowMem      = false
	defaultShowDisk     = false
	defaultShowUptime   = false
	defaultAlign        = "right"
	defaultOffsetX      = 8
	defaultMarginTop    = 0
	defaultMarginV      = 0
	defaultLayout       = "two-line"
	defaultNumAlign     = "left"
	defaultSpeedUnit    = "B"
	defaultUnitSpace    = true
	defaultFontFamily   = "Microsoft YaHei"
	defaultFontSize     = 9
	defaultFGColor      = "#FFFFFF"
	defaultBGMode       = "theme"
	defaultBGColor      = "#1E1E1E"
	defaultFollowTheme  = true
	defaultSeparator    = "space"
	defaultRender       = "gdi"
	defaultAvoidWidgets = true
	defaultMultiMonitor = false
)

// Sampling bounds: below 200ms the sampler costs more CPU than it reports on.
const (
	minInterval = 200 * time.Millisecond
	maxInterval = 10 * time.Second
)

// stopWait bounds how long Stop waits for its goroutines.
const stopWait = 2 * time.Second

// Action ids surfaced in the preferences panel.
const (
	actionToggle = "toggle_widget"
	actionReset  = "reset_position"
	actionCopy   = "copy_stats"
)

// Feature implements the taskbar status module.
type Feature struct {
	core.Base

	ctx       *core.Context
	collector *Collector

	mu      sync.RWMutex
	running bool
	win     *widget
	last    Stats
	hidden  bool
}

// NewFeature constructs the taskbar module.
func NewFeature() core.Module { return &Feature{} }

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "任务栏状态统计" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "在任务栏内嵌显示 CPU / 内存 / 网络速率 / 磁盘占用（TrafficMonitor 风格）"
}

// Options implements core.Module: the widget's settings (PRD §7 配置总表).
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optInterval, Label: "刷新间隔 (毫秒)", Kind: core.KindInt,
			Default: defaultInterval, Min: 200, Max: 10000, Step: 100,
			Help: "数值越小越实时，但采样本身也会占用 CPU"},
		{Key: optShowDown, Label: "显示下载速率", Kind: core.KindBool, Default: defaultShowDown},
		{Key: optShowUp, Label: "显示上传速率", Kind: core.KindBool, Default: defaultShowUp},
		{Key: optShowCPU, Label: "显示 CPU 占用", Kind: core.KindBool, Default: defaultShowCPU},
		{Key: optShowMem, Label: "显示内存占用", Kind: core.KindBool, Default: defaultShowMem},
		{Key: optShowDisk, Label: "显示磁盘占用", Kind: core.KindBool, Default: defaultShowDisk},
		{Key: optShowUptime, Label: "显示运行时长", Kind: core.KindBool, Default: defaultShowUptime},
		{Key: optAlign, Label: "对齐方式", Kind: core.KindSelect, Default: defaultAlign,
			Choices: []core.Choice{
				{Value: "left", Label: "左对齐"},
				{Value: "center", Label: "居中"},
				{Value: "right", Label: "右对齐"},
			}},
		{Key: optOffsetX, Label: "水平偏移 (px)", Kind: core.KindInt,
			Default: defaultOffsetX, Min: -400, Max: 400, Step: 1,
			Help: "距离任务栏右侧的距离，用于避开其他托盘组件"},
		{Key: optMarginTop, Label: "顶部留白 (px)", Kind: core.KindInt,
			Default: defaultMarginTop, Min: -20, Max: 40, Step: 1},
		{Key: optMarginV, Label: "垂直留白 (px)", Kind: core.KindInt,
			Default: defaultMarginV, Min: -20, Max: 40, Step: 1},
		{Key: optLayout, Label: "排版", Kind: core.KindSelect, Default: defaultLayout,
			Choices: []core.Choice{
				{Value: "single-line", Label: "单行"},
				{Value: "two-line", Label: "双行"},
			},
			Help: "双行在任务栏较高时更易读", Restart: true},
		{Key: optNumAlign, Label: "数字对齐", Kind: core.KindSelect, Default: defaultNumAlign,
			Choices: []core.Choice{
				{Value: "left", Label: "数字左对齐"},
				{Value: "right", Label: "数字右对齐"},
			}},
		{Key: optSpeedUnit, Label: "速率单位", Kind: core.KindSelect, Default: defaultSpeedUnit,
			Choices: []core.Choice{
				{Value: "B", Label: "字节 B/s（自动进位）"},
				{Value: "b", Label: "比特 b/s（自动进位）"},
				{Value: "fixedKB", Label: "固定 KB/s"},
			}},
		{Key: optUnitSpace, Label: "数值与单位间加空格", Kind: core.KindBool, Default: defaultUnitSpace},
		{Key: optFontFamily, Label: "字体", Kind: core.KindString, Default: defaultFontFamily},
		{Key: optFontSize, Label: "字号", Kind: core.KindInt,
			Default: defaultFontSize, Min: 6, Max: 24, Step: 1},
		{Key: optFGColor, Label: "文字颜色", Kind: core.KindColor, Default: defaultFGColor},
		{Key: optBGMode, Label: "背景模式", Kind: core.KindSelect, Default: defaultBGMode,
			Choices: []core.Choice{
				{Value: "theme", Label: "跟随系统主题"},
				{Value: "solid", Label: "纯色"},
			}},
		{Key: optBGColor, Label: "背景颜色", Kind: core.KindColor, Default: defaultBGColor,
			VisibleIf: &core.VisibleIf{Key: optBGMode, Value: "solid"}},
		{Key: optFollowTheme, Label: "跟随明暗主题", Kind: core.KindBool, Default: defaultFollowTheme},
		{Key: optSeparator, Label: "分隔符", Kind: core.KindSelect, Default: defaultSeparator,
			Choices: []core.Choice{
				{Value: "space", Label: "空格"},
				{Value: "pipe", Label: "竖线 |"},
				{Value: "dot", Label: "间隔点 ·"},
				{Value: "none", Label: "无"},
			}},
		{Key: optRender, Label: "渲染方式", Kind: core.KindSelect, Default: defaultRender,
			Choices: []core.Choice{
				{Value: "gdi", Label: "GDI"},
			},
			Help: "当前仅内置 GDI 渲染", Restart: true},
		{Key: optAvoidWidgets, Label: "避开其他任务栏组件", Kind: core.KindBool, Default: defaultAvoidWidgets},
		{Key: optMultiMonitor, Label: "在副屏任务栏也显示", Kind: core.KindBool, Default: defaultMultiMonitor},
	}
}

// Actions implements core.Module: the panel buttons for this module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionToggle, Label: "显示/隐藏小组件", Kind: core.ActionNormal,
			Group: "小组件", Description: "切换任务栏小组件的可见性"},
		{ID: actionReset, Label: "重置位置", Kind: core.ActionNormal,
			Group: "小组件", Description: "把水平偏移、留白恢复为默认值"},
	}
}

// Init implements core.Module: it stores the context and builds the sampler.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errFeatureNil
	}
	f.ctx = ctx
	f.collector = NewCollector(ctx, f.interval())
	return nil
}

// Start implements core.Module: begins sampling and shows the widget.
func (f *Feature) Start() error {
	if f.ctx == nil {
		return errFeatureNil
	}
	if !f.ctx.Config.Enabled() {
		return nil
	}

	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return nil
	}
	f.running = true
	f.hidden = false
	f.mu.Unlock()

	// A fresh collector per Start keeps interval changes simple and makes a
	// restart after ApplyOption leave no stale sampler behind.
	f.collector = NewCollector(f.ctx, f.interval())
	go f.collector.Run()
	go f.pump()

	if platformNative() {
		f.spawnWidget()
	}

	f.ctx.Logger.Info("任务栏状态已启动", "module", moduleID,
		"interval_ms", f.interval().Milliseconds(), "native", platformNative())
	f.ctx.Bus.Log(moduleID, "info", "任务栏状态统计已启动")
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// Stop implements core.Module. It is idempotent and never blocks forever.
func (f *Feature) Stop() error {
	f.mu.Lock()
	if !f.running {
		f.mu.Unlock()
		return nil
	}
	f.running = false
	w := f.win
	f.win = nil
	f.mu.Unlock()

	if f.collector != nil {
		f.collector.Stop()
	}
	if w != nil {
		w.Stop()
	}

	if f.ctx != nil {
		f.ctx.Logger.Info("任务栏状态已停止", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// State implements core.Module: the live metrics for the panel.
func (f *Feature) State() core.State {
	f.mu.RLock()
	running, hidden := f.running, f.hidden
	s := f.last
	f.mu.RUnlock()

	state := core.State{
		"running":       running,
		"visible":       running && !hidden,
		"native":        platformNative(),
		"interval_ms":   int(f.interval().Milliseconds()),
		"cpu_percent":   round1(s.CPU),
		"mem_percent":   round1(s.RAM),
		"disk_percent":  round1(s.Disk),
		"upload_rate":   f.rate(s.NetUp),
		"download_rate": f.rate(s.NetDn),
		"uptime":        s.Uptime.Truncate(time.Second).String(),
		"window":        describeWindow(),
	}
	return state
}

// OnHotkey implements core.Module: toggles the widget's visibility.
func (f *Feature) OnHotkey() error {
	if f.ctx == nil {
		return errFeatureNil
	}
	f.mu.Lock()
	running, hidden := f.running, f.hidden
	w := f.win
	f.mu.Unlock()

	if !running {
		// Not sampling: starting the module is the most useful response.
		return f.Start()
	}
	if hidden {
		f.mu.Lock()
		f.hidden = false
		f.mu.Unlock()
		if w == nil && platformNative() {
			f.spawnWidget()
		}
		f.ctx.Bus.Notice(moduleID, "任务栏小组件已显示")
		f.ctx.Bus.State(moduleID, f.State())
		return nil
	}

	f.mu.Lock()
	f.hidden = true
	f.win = nil
	f.mu.Unlock()
	if w != nil {
		w.Stop()
	}
	f.ctx.Bus.Notice(moduleID, "任务栏小组件已隐藏")
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// OpenUI implements core.Module: reveals the widget, starting it if needed.
func (f *Feature) OpenUI() error { return f.OnHotkey() }

// ApplyOption implements core.Module: reacts to live setting changes.
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return errFeatureNil
	}
	switch key {
	case optInterval:
		ms, ok := toInt(value)
		if !ok || ms <= 0 {
			return errInterval
		}
		if f.collector != nil {
			f.collector.SetInterval(intervalOf(ms))
		}
	case optShowDown, optShowUp, optShowCPU, optShowMem, optShowDisk, optShowUptime,
		optAlign, optOffsetX, optMarginTop, optMarginV, optNumAlign, optSpeedUnit,
		optUnitSpace, optFontFamily, optFontSize, optFGColor, optBGMode, optBGColor,
		optFollowTheme, optSeparator, optAvoidWidgets, optMultiMonitor:
		f.rebuildWidget()
	case optLayout, optRender:
		// Declared with Restart: true — the app restarts this module, rebuilding
		// here too would race with that restart.
	default:
		return &unknownOptionError{key: key}
	}
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// RunAction executes a declared panel action.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case actionToggle:
		return f.OnHotkey()
	case actionReset:
		if err := f.ctx.Config.Set(optOffsetX, defaultOffsetX); err != nil {
			return err
		}
		f.rebuildWidget()
		return nil
	}
	return &unknownActionError{id: id}
}

// pump forwards samples to the widget, or logs them when there is none.
func (f *Feature) pump() {
	ch := f.collector.Channel()
	appDone := f.ctx.Ctx.Done()
	for {
		select {
		case <-appDone:
			return
		case s, ok := <-ch:
			if !ok {
				return
			}
			f.mu.Lock()
			f.last = s
			w := f.win
			f.mu.Unlock()

			if w != nil {
				w.SetStats(s)
				continue
			}
			// No native widget: keep the numbers visible in the panel/log.
			f.ctx.Logger.Debug("系统状态采样", "module", moduleID,
				"cpu_percent", pct(s.CPU), "mem_percent", pct(s.RAM),
				"upload", f.rate(s.NetUp), "download", f.rate(s.NetDn))
		}
	}
}

// rebuildWidget recreates the native widget so appearance changes apply.
func (f *Feature) rebuildWidget() {
	if !platformNative() {
		return
	}
	f.mu.Lock()
	if !f.running || f.hidden {
		f.mu.Unlock()
		return
	}
	w := f.win
	f.win = nil
	f.mu.Unlock()

	if w != nil {
		w.Stop()
	}
	f.spawnWidget()
}

// spawnWidget creates the native widget on its own goroutine.
func (f *Feature) spawnWidget() {
	w := newWidget(f)
	f.mu.Lock()
	f.win = w
	f.mu.Unlock()

	go w.Run()
}

// reportError logs a widget failure through the module's own channels.
func (f *Feature) reportError(msg string, err error) {
	if f.ctx == nil {
		return
	}
	f.ctx.Logger.Error(msg, "module", moduleID, "err", err)
	f.ctx.Bus.Log(moduleID, "error", msg+": "+err.Error())
}

// interval reads the configured refresh interval, clamped to safe bounds.
func (f *Feature) interval() time.Duration {
	return intervalOf(f.intOpt(optInterval, defaultInterval))
}

// intervalOf converts milliseconds into a clamped duration.
func intervalOf(ms int) time.Duration {
	d := time.Duration(ms) * time.Millisecond
	if d < minInterval {
		return minInterval
	}
	if d > maxInterval {
		return maxInterval
	}
	return d
}

// offsetX reads the configured horizontal offset.
func (f *Feature) offsetX() int {
	return f.intOpt(optOffsetX, defaultOffsetX)
}

// intOpt reads an integer option, falling back to def when unset/invalid.
func (f *Feature) intOpt(key string, def int) int {
	if f.ctx == nil {
		return def
	}
	if n, ok := toInt(f.ctx.Config.Get(key, def)); ok {
		return n
	}
	return def
}

// boolOpt reads a boolean option, falling back to def when unset/invalid.
func (f *Feature) boolOpt(key string, def bool) bool {
	if f.ctx == nil {
		return def
	}
	if b, ok := f.ctx.Config.Get(key, def).(bool); ok {
		return b
	}
	return def
}

// stringOpt reads a string option, falling back to def when unset/invalid.
func (f *Feature) stringOpt(key string, def string) string {
	if f.ctx == nil {
		return def
	}
	if s, ok := f.ctx.Config.Get(key, def).(string); ok && s != "" {
		return s
	}
	return def
}

// rate formats a bytes/sec value according to the configured unit.
//
// "B" and "b" auto-scale the prefix (KB/MB/GB); "fixedKB" always reports KB/s,
// which is what TrafficMonitor offers for users comparing against a router.
func (f *Feature) rate(bps float64) string {
	if bps < 0 {
		bps = 0
	}
	sep := f.unitSep()
	switch f.stringOpt(optSpeedUnit, defaultSpeedUnit) {
	case "b":
		// Bits share the same prefixes; only the suffix differs.
		return humanScale(bps*8) + sep + "b/s"
	case "fixedKB":
		return humanScale(bps/1024) + sep + "KB/s"
	default:
		return humanScale(bps) + sep + "B/s"
	}
}

// unitSep returns the configured spacing between value and unit.
func (f *Feature) unitSep() string {
	if f.boolOpt(optUnitSpace, defaultUnitSpace) {
		return " "
	}
	return ""
}

// itoa is a small convenience wrapper used by the formatters above.
func itoa(n int) string { return strconv.Itoa(n) }

// pct formats a percentage without decimals.
func pct(v float64) string { return strconv.Itoa(int(v+0.5)) + "%" }

// round1 rounds to one decimal place for display.
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// toInt coerces the numeric shapes a YAML/JSON config value can arrive in.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
