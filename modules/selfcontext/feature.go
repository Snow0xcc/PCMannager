// Package selfcontext implements a MineContext-lite recorder: it samples the
// active window title over time so the user can review "what I was working on"
// and paste a compact context summary into an LLM.
//
// The module records screen-derived information, so it is privacy sensitive and
// ships disabled (internal/config.Default sets Enabled=false, PRD SC-09).
package selfcontext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// moduleID is the stable identifier used by the registry, the configuration
// file and hotkey bindings; it must match the directory name.
const moduleID = "selfcontext"

// Option keys, kept in sync with internal/config.Default().
const (
	optInterval      = "interval"
	optRetentionDays = "retention_days"
	optCaptureMode   = "capture_mode"
	optPauseOnLock   = "pause_on_lock"
)

// Option defaults (mirrors internal/config.Default()).
const (
	defaultInterval      = 300
	defaultRetentionDays = 7
	defaultCaptureMode   = "primary"
	defaultPauseOnLock   = true
)

// Capture modes.
const (
	// modeTitle records only the active window title (no screen content).
	modeTitle = "title"
	// modePrimary records the active window title plus the owning process.
	modePrimary = "primary"
)

// Sampling bounds: the configured interval is clamped so a mistyped value
// cannot turn the recorder into a busy loop.
const (
	minInterval = 50
	maxInterval = 60_000
	// maxEntries bounds the in-memory ring buffer.
	maxEntries = 500
)

// Action ids surfaced in the preferences panel.
const (
	actionOpen   = "open"
	actionCopy   = "copy_summary"
	actionClear  = "clear"
	actionExport = "export"
)

// Module-level errors.
var (
	errNoFeature = errors.New("selfcontext: 模块未初始化")
)

// Feature implements the MineContext-lite activity recorder.
type Feature struct {
	core.Base

	ctx *core.Context

	mu      sync.Mutex
	entries []Entry
	stopCh  chan struct{}
	running bool
	// last is the most recently recorded title, used for de-duplication.
	last string
	// paused reflects pause_on_lock: sampling is suspended while the session
	// is locked so a lock screen is never recorded.
	paused bool
}

// Entry is one sampled active-window context record.
type Entry struct {
	Title     string
	Process   string
	Timestamp time.Time
}

// NewFeature constructs the selfcontext module.
func NewFeature() core.Module { return &Feature{} }

// Compile-time proof that Feature satisfies the module contract.
var _ core.Module = (*Feature)(nil)

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "上下文记录" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "MineContext 式上下文记录：定期采样活动窗口，生成可粘贴给 LLM 的摘要"
}

// Options implements core.Module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optInterval, Label: "采样间隔(ms)", Kind: core.KindInt,
			Default: defaultInterval, Min: minInterval, Max: maxInterval, Step: 50,
			Help: "采样活动窗口的间隔", Restart: true},
		{Key: optRetentionDays, Label: "保留天数", Kind: core.KindInt,
			Default: defaultRetentionDays, Min: 1, Max: 365, Step: 1,
			Help: "丢弃早于该天数的记录"},
		{Key: optCaptureMode, Label: "采集模式", Kind: core.KindSelect,
			Default: defaultCaptureMode,
			Choices: []core.Choice{
				{Value: modePrimary, Label: "窗口 + 进程名"},
				{Value: modeTitle, Label: "仅窗口标题"},
			},
			Help: "采样记录的详细程度；从不采集屏幕图像"},
		{Key: optPauseOnLock, Label: "锁屏时暂停", Kind: core.KindBool,
			Default: defaultPauseOnLock,
			Help:    "会话锁定时停止记录，避免采集锁屏内容"},
	}
}

// Actions implements core.Module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionOpen, Label: "查看上下文记录", Kind: core.ActionOpen,
			Description: "显示最近的活动窗口时间线"},
		{ID: actionCopy, Label: "复制摘要到剪贴板", Kind: core.ActionNormal,
			Description: "生成一段可直接粘贴给 LLM 的上下文摘要"},
		{ID: actionExport, Label: "导出为文本文件", Kind: core.ActionNormal,
			Description: "把当前记录写入模块数据目录"},
		{ID: actionClear, Label: "清空上下文记录", Kind: core.ActionDanger, Confirm: true,
			Description: "删除全部已记录的活动窗口"},
	}
}

// Init implements core.Module.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errors.New("selfcontext: 模块上下文为空")
	}
	f.ctx = ctx
	f.prune()
	return nil
}

// Start implements core.Module: it begins sampling the active window.
func (f *Feature) Start() error {
	if f.ctx == nil {
		return errNoFeature
	}
	if !f.ctx.Config.Enabled() {
		f.ctx.Logger.Info("上下文记录已禁用（隐私开关），跳过启动", "module", moduleID)
		return nil
	}
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return nil
	}
	stop := make(chan struct{})
	f.stopCh = stop
	f.running = true
	f.mu.Unlock()

	go f.sample(stop)

	f.ctx.Logger.Info("上下文记录已启动", "module", moduleID,
		"interval_ms", f.interval(), "mode", f.captureMode())
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// Stop implements core.Module. It is idempotent.
func (f *Feature) Stop() error {
	if f.ctx == nil {
		return nil
	}
	f.mu.Lock()
	stop := f.stopCh
	f.stopCh = nil
	wasRunning := f.running
	f.running = false
	f.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if wasRunning {
		f.ctx.Logger.Info("上下文记录已停止", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// State implements core.Module: the panel-facing snapshot.
func (f *Feature) State() core.State {
	f.mu.Lock()
	count := len(f.entries)
	running := f.running
	paused := f.paused
	last := f.last
	f.mu.Unlock()
	return core.State{
		"running":        running,
		"paused":         paused,
		"count":          count,
		"last_title":     last,
		"interval_ms":    f.interval(),
		"capture_mode":   f.captureMode(),
		"retention_days": f.retentionDays(),
		"pause_on_lock":  f.boolOpt(optPauseOnLock, defaultPauseOnLock),
	}
}

// OnHotkey implements core.Module: the hotkey opens the context viewer.
func (f *Feature) OnHotkey() error { return f.OpenUI() }

// OpenUI implements core.Module. On Windows it shows the native viewer, which
// runs on its own OS thread; other platforms report through the panel.
func (f *Feature) OpenUI() error {
	if f.ctx == nil {
		return errNoFeature
	}
	f.ctx.Bus.State(moduleID, f.State())
	return showContext(f)
}

// ApplyOption implements core.Module.
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return errNoFeature
	}
	switch key {
	case optInterval:
		n, ok := toInt(value)
		if !ok || n < minInterval || n > maxInterval {
			return fmt.Errorf("selfcontext: %s 需要在 %d..%d 之间", key, minInterval, maxInterval)
		}
	case optRetentionDays:
		n, ok := toInt(value)
		if !ok || n <= 0 {
			return fmt.Errorf("selfcontext: %s 需要正整数", key)
		}
		f.prune()
	case optCaptureMode:
		s, ok := value.(string)
		if !ok || (s != modeTitle && s != modePrimary) {
			return fmt.Errorf("selfcontext: %s 需要是 %s 或 %s", key, modeTitle, modePrimary)
		}
	case optPauseOnLock:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("selfcontext: %s 需要布尔值", key)
		}
	default:
		return fmt.Errorf("selfcontext: 未知配置项 %s", key)
	}
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// RunAction executes a declared panel action.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case actionOpen:
		return f.OpenUI()
	case actionCopy:
		if err := copyToClipboard(f.Summary()); err != nil {
			return err
		}
		f.ctx.Logger.Info("已复制上下文摘要", "module", moduleID)
		f.ctx.Bus.Notice(moduleID, "上下文摘要已复制到剪贴板")
		return nil
	case actionExport:
		path, err := f.Export()
		if err != nil {
			return err
		}
		f.ctx.Bus.Notice(moduleID, "已导出上下文记录："+path)
		return nil
	case actionClear:
		f.Clear()
		f.ctx.Logger.Info("已清空上下文记录", "module", moduleID)
		f.ctx.Bus.Notice(moduleID, "已清空上下文记录")
		return nil
	}
	return fmt.Errorf("selfcontext: 未知动作 %s", id)
}

// Export writes the current records to a timestamped text file in the module's
// data directory and returns its path. It backs the "export" panel action and
// works on every platform.
func (f *Feature) Export() (string, error) {
	if f.ctx == nil {
		return "", errNoFeature
	}
	dir := f.ctx.DataDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "PCMannager", moduleID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-context.txt")
	if err := os.WriteFile(path, []byte(f.Summary()), 0o600); err != nil {
		return "", err
	}
	f.ctx.Logger.Info("已导出上下文记录", "module", moduleID, "path", path)
	return path, nil
}

// sample records the active window until stop is closed or the app shuts down.
func (f *Feature) sample(stop chan struct{}) {
	interval := time.Duration(f.interval()) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-f.ctx.Ctx.Done():
			return
		case <-ticker.C:
			f.record()
		}
	}
}

// record takes one sample, skipping repeats, locked sessions and empty titles.
func (f *Feature) record() {
	if f.ctx == nil {
		return
	}
	title, process, err := ActiveWindow()
	if err != nil {
		// A transient Win32 failure is not worth a log line per tick.
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	if f.boolOpt(optPauseOnLock, defaultPauseOnLock) && sessionLocked() {
		f.setPaused(true)
		return
	}
	f.setPaused(false)

	f.mu.Lock()
	if title == f.last {
		f.mu.Unlock()
		return
	}
	f.last = title
	if f.captureMode() == modeTitle {
		// "title" mode records nothing but the window caption.
		process = ""
	}
	f.entries = append(f.entries, Entry{Title: title, Process: process, Timestamp: time.Now()})
	if len(f.entries) > maxEntries {
		f.entries = append(f.entries[:0], f.entries[len(f.entries)-maxEntries:]...)
	}
	f.mu.Unlock()
	f.prune()
}

// setPaused records whether sampling is currently suspended.
func (f *Feature) setPaused(paused bool) {
	f.mu.Lock()
	f.paused = paused
	f.mu.Unlock()
}

// Clear drops every recorded entry.
func (f *Feature) Clear() {
	f.mu.Lock()
	f.entries = nil
	f.last = ""
	f.mu.Unlock()
	if f.ctx != nil && f.ctx.Bus != nil {
		f.ctx.Bus.State(moduleID, f.State())
	}
}

// Snapshot returns a copy of the recorded context entries.
func (f *Feature) Snapshot() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Entry, len(f.entries))
	copy(out, f.entries)
	return out
}

// Summary renders a compact plain-text context summary (for pasting to an LLM).
func (f *Feature) Summary() string {
	es := f.Snapshot()
	if len(es) == 0 {
		return "(无记录)"
	}
	var b strings.Builder
	b.WriteString("最近的工作上下文：\n")
	for _, e := range es {
		b.WriteString("- ")
		b.WriteString(e.Timestamp.Format("15:04:05"))
		b.WriteString(" ")
		b.WriteString(e.Title)
		if e.Process != "" {
			b.WriteString(" [")
			b.WriteString(e.Process)
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// prune drops entries older than the configured retention window.
func (f *Feature) prune() {
	f.mu.Lock()
	defer f.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -f.retentionDays())
	kept := f.entries[:0]
	for _, e := range f.entries {
		if e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		}
	}
	f.entries = kept
}

// interval returns the sampling interval in milliseconds, clamped.
func (f *Feature) interval() int {
	n, ok := toInt(f.get(optInterval, defaultInterval))
	if !ok || n < minInterval {
		return defaultInterval
	}
	if n > maxInterval {
		return maxInterval
	}
	return n
}

// retentionDays returns the configured retention window in days.
func (f *Feature) retentionDays() int {
	if n, ok := toInt(f.get(optRetentionDays, defaultRetentionDays)); ok && n > 0 {
		return n
	}
	return defaultRetentionDays
}

// captureMode returns the configured capture mode.
func (f *Feature) captureMode() string {
	if s, ok := f.get(optCaptureMode, defaultCaptureMode).(string); ok && s != "" {
		return s
	}
	return defaultCaptureMode
}

// boolOpt reads a boolean option with a fallback.
func (f *Feature) boolOpt(key string, def bool) bool {
	if v, ok := f.get(key, def).(bool); ok {
		return v
	}
	return def
}

// get reads an option, tolerating a module that is not initialised yet.
func (f *Feature) get(key string, def any) any {
	if f.ctx == nil || f.ctx.Config == nil {
		return def
	}
	return f.ctx.Config.Get(key, def)
}

// toInt narrows the numeric shapes a YAML/JSON config value can arrive in.
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
