// Package selfcontext implements a MineContext-lite recorder: it samples the
// active window title over time so the user can review "what I was working on"
// and paste a compact context summary into an LLM.
//
// The module records screen-derived information, so it is privacy sensitive and
// ships disabled (internal/config.Default sets Enabled=false, PRD SC-09).
package selfcontext

import (
	"encoding/json"
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

	// 阶段一/二：屏幕捕获 + VLM 语义解析（MineContext 完整形态）。
	optVLMBaseURL = "vlm_base_url"
	optVLMModel   = "vlm_model"
	optVLMKey     = "vlm_api_key"
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
	// modeScreen 周期性截屏并交给 VLM 生成一句话语义描述（隐私最重，
	// 因此默认关闭 + 仅在用户显式选择该模式时才请求屏幕内容）。
	modeScreen = "screen"
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

// historyFileName is the JSON file in the module data directory that carries
// the recorded entries across restarts.
const historyFileName = "context_history.json"

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
	// activeWindow is injectable so tests can drive record() without a real
	// window system; nil means use the platform probe.
	activeWindow func() (title, process string, err error)
}

// Entry is one sampled active-window context record.
type Entry struct {
	Title     string    `json:"title"`
	Process   string    `json:"process,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	// Summary 是 modeScreen 下 VLM 生成的画面描述（阶段二产物），
	// 其余模式为空。截图本体不落盘也不入库（阶段二的向量库留给后续）。
	Summary string `json:"summary,omitempty"`
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
				{Value: modeScreen, Label: "屏幕画面 + AI 描述（需配置 VLM）"},
			},
			Help: "屏幕模式会周期性截屏并发给 VLM 解析，隐私敏感，请谨慎开启"},
		{Key: optVLMBaseURL, Label: "VLM 服务地址", Kind: core.KindString, Default: "",
			VisibleIf: &core.VisibleIf{Key: optCaptureMode, Value: modeScreen},
			Help:      "OpenAI 兼容端点，如 http://127.0.0.1:1234/v1（LM Studio）"},
		{Key: optVLMModel, Label: "VLM 模型名", Kind: core.KindString, Default: "",
			VisibleIf: &core.VisibleIf{Key: optCaptureMode, Value: modeScreen},
			Help:      "视觉模型，如 qwen2-vl-7b-instruct / gpt-4o-mini"},
		{Key: optVLMKey, Label: "VLM API Key", Kind: core.KindString, Default: "",
			VisibleIf: &core.VisibleIf{Key: optCaptureMode, Value: modeScreen},
			Help:      "本地服务可留空；云端服务必填"},
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

// Init implements core.Module. It restores the persisted history so records
// survive a restart, then trims it to the retention window and entry cap.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errors.New("selfcontext: 模块上下文为空")
	}
	f.ctx = ctx
	f.load()
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
		f.pruneAndSave()
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
	dir := f.dataDir()
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
	probe := f.activeWindow
	if probe == nil {
		probe = ActiveWindow
	}
	title, process, err := probe()
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
	mode := f.captureMode()
	if mode == modeTitle {
		// "title" mode records nothing but the window caption.
		process = ""
	}
	e := Entry{Title: title, Process: process, Timestamp: time.Now()}
	f.entries = append(f.entries, e)
	if len(f.entries) > maxEntries {
		f.entries = append(f.entries[:0], f.entries[len(f.entries)-maxEntries:]...)
	}
	// Persist under the same lock section so the file always matches the
	// latest in-memory state; the write is small and only happens on a new
	// distinct title, so holding f.mu through the disk write is cheap.
	f.pruneLocked()
	f.saveLocked()
	f.mu.Unlock()

	// 阶段一/二：screen 模式在同一 tick 里追加一次截图 + VLM 解析。
	// 放在锁外（网络调用最长 60s，不能阻塞采样循环与 State 查询），
	// 解析完成后由 recordSummary 把描述补写到对应条目上。
	// 注：prune 不用在这里再调一次——上面的 pruneLocked 已在同一临界区里
	// 裁剪过环形缓冲，重复调用既多余又会在锁外二次改 entries。
	if mode == modeScreen {
		go f.recordSummary(title)
	}
}

// setPaused records whether sampling is currently suspended.
func (f *Feature) setPaused(paused bool) {
	f.mu.Lock()
	f.paused = paused
	f.mu.Unlock()
}

// Clear drops every recorded entry and persists the empty state.
func (f *Feature) Clear() {
	f.mu.Lock()
	f.entries = nil
	f.last = ""
	f.saveLocked()
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

// prune drops expired entries from memory only.
func (f *Feature) prune() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneLocked()
}

// pruneAndSave applies the retention window and persists the result; used when
// retention_days changes at runtime.
func (f *Feature) pruneAndSave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneLocked()
	f.saveLocked()
}

// pruneLocked drops entries older than the configured retention window.
// Callers must hold f.mu.
func (f *Feature) pruneLocked() {
	cutoff := time.Now().AddDate(0, 0, -f.retentionDays())
	kept := f.entries[:0]
	for _, e := range f.entries {
		if e.Timestamp.After(cutoff) {
			kept = append(kept, e)
		}
	}
	f.entries = kept
}

// load restores the persisted history from the module data directory, then
// trims it to the retention window and entry cap and rewrites the file, so a
// restart never resurrects expired entries. A missing file is the normal
// first-run case; a corrupt file is dropped with a warning so a damaged write
// can never crash the app.
func (f *Feature) load() {
	path := filepath.Join(f.dataDir(), historyFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			f.ctx.Logger.Warn("读取上下文历史失败，从空记录开始", "module", moduleID, "path", path, "err", err)
		}
		return
	}
	var loaded []Entry
	if err := json.Unmarshal(data, &loaded); err != nil {
		f.ctx.Logger.Warn("上下文历史文件损坏，忽略并从空记录开始",
			"module", moduleID, "path", path, "err", err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = loaded
	if len(f.entries) > maxEntries {
		f.entries = append(f.entries[:0], f.entries[len(f.entries)-maxEntries:]...)
	}
	f.pruneLocked()
	f.saveLocked()
}

// save persists the current history to disk.
func (f *Feature) save() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveLocked()
}

// saveLocked atomically writes the in-memory history to the module data
// directory. Callers must hold f.mu so the file can never be overwritten by a
// stale snapshot nor observed mid-write.
func (f *Feature) saveLocked() {
	if f.ctx == nil {
		return
	}
	dir := f.dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.ctx.Logger.Warn("无法创建上下文记录目录", "module", moduleID, "dir", dir, "err", err)
		return
	}
	entries := f.entries
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		f.ctx.Logger.Warn("序列化上下文历史失败", "module", moduleID, "err", err)
		return
	}
	path := filepath.Join(dir, historyFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		f.ctx.Logger.Warn("上下文历史落盘失败", "module", moduleID, "path", tmp, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows can refuse rename while the target is momentarily locked;
		// fall back to a direct write so the user's history is not lost.
		if werr := os.WriteFile(path, data, 0o600); werr != nil {
			f.ctx.Logger.Warn("上下文历史落盘失败", "module", moduleID, "path", path, "err", werr)
		}
	}
}

// dataDir returns the directory the history file lives in: the module data
// directory, else a folder under the user's config directory.
func (f *Feature) dataDir() string {
	if f.ctx != nil && f.ctx.DataDir != "" {
		return f.ctx.DataDir
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "PCMannager", moduleID)
	}
	return "."
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
