// Package clipboard implements the Ditto-style clipboard history module: it
// records what the user copies, keeps a bounded history and can write any
// entry back onto the system clipboard.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
)

// Action id for writing the newest entry back (declared by Actions()).
const actionWriteLast = "write_last"

// Timings.
const (
	// echoWindow is how long a write-back is ignored by the watcher, so the
	// entry this module just pushed is not recorded again as a new one.
	echoWindow = 3 * time.Second
	// writeTimeout bounds a single write-back attempt.
	writeTimeout = 2 * time.Second
	// maxPreview is the length of the excerpt published to the panel.
	maxPreview = 120
)

// Module-level errors.
var (
	errNoFeature = errors.New("clipboard: 模块不可用")
)

// errWindowCreate wraps a window creation failure.
func errWindowCreate(err error) error {
	return fmt.Errorf("clipboard: 创建剪贴板历史窗口失败: %w", err)
}

// Feature implements a Ditto-style clipboard history manager.
type Feature struct {
	core.Base

	ctx  *core.Context
	hist *History

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	// echo* remembers the last payload this module wrote so the watcher can
	// skip it instead of re-adding it to the history.
	echoText  string
	echoImage []byte
	echoAt    time.Time
}

// NewFeature constructs the clipboard manager module.
func NewFeature() core.Module { return &Feature{hist: NewHistory(defaultMaxItems)} }

// Compile-time proof that Feature satisfies the module contract.
var _ core.Module = (*Feature)(nil)

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "剪贴板历史" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "Ditto 式剪贴板历史：自动记录复制的文本与图片，可随时写回"
}

// Options implements core.Module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optMaxItems, Label: "历史条数上限", Kind: core.KindInt,
			Default: defaultMaxItems, Min: 10, Max: 5000, Step: 10,
			Help: "超出上限后丢弃最旧的未固定条目"},
		{Key: optStoreImages, Label: "记录图片", Kind: core.KindBool,
			Default: defaultStoreImages,
			Help:    "同时记录复制的图片（PNG）", Restart: true},
		{Key: optPasteOnCopy, Label: "写回后自动粘贴", Kind: core.KindBool,
			Default: defaultPasteOnCopy,
			Help:    "写回剪贴板后向前台窗口发送 Ctrl+V（尽力而为）"},
		{Key: optRetentionDays, Label: "保留天数", Kind: core.KindInt,
			Default: defaultRetentionDays, Min: 1, Max: 365, Step: 1,
			Help: "启动时丢弃早于该天数的未固定条目"},
	}
}

// Actions implements core.Module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionOpen, Label: "打开剪贴板历史", Kind: core.ActionOpen,
			Description: "显示最近复制的内容，可写回任意一条"},
		{ID: actionWriteLast, Label: "写回最近一条", Kind: core.ActionNormal,
			Description: "把最新一条历史写回系统剪贴板"},
		{ID: actionClear, Label: "清空剪贴板历史", Kind: core.ActionDanger, Confirm: true,
			Description: "删除全部未固定的条目"},
	}
}

// Init implements core.Module.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errors.New("clipboard: 模块上下文为空")
	}
	f.ctx = ctx
	f.applyConfig()
	f.hist.SetOnChange(func() {
		if f.ctx != nil && f.ctx.Bus != nil {
			f.ctx.Bus.State(moduleID, f.State())
		}
	})
	return nil
}

// Start implements core.Module: it opens the clipboard and begins watching.
func (f *Feature) Start() error {
	if f.ctx == nil {
		return errors.New("clipboard: 模块未初始化")
	}
	if !f.ctx.Config.Enabled() {
		return nil
	}
	if err := clip.Init(); err != nil {
		f.ctx.Logger.Error("剪贴板不可用", "module", moduleID, "err", err)
		f.ctx.Bus.Log(moduleID, "error", fmt.Sprintf("剪贴板初始化失败: %v", err))
		return err
	}

	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return nil
	}
	cfg := f.snapshot()
	ctx, cancel := context.WithCancel(f.ctx.Ctx)
	f.cancel = cancel
	f.running = true
	f.mu.Unlock()

	go f.watch(ctx, cfg)

	f.ctx.Logger.Info("剪贴板历史已启动", "module", moduleID,
		"max_items", cfg.MaxItems, "store_images", cfg.StoreImages)
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// Stop implements core.Module. It is idempotent.
func (f *Feature) Stop() error {
	if f.ctx == nil {
		return nil
	}
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	wasRunning := f.running
	f.running = false
	f.mu.Unlock()

	if wasRunning {
		f.ctx.Logger.Info("剪贴板历史已停止", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// State implements core.Module: the panel-facing snapshot.
func (f *Feature) State() core.State {
	cfg := f.snapshot()
	entries := f.hist.All()
	last, lastKind := "", string(KindText)
	if n := len(entries); n > 0 {
		last = summary(entries[n-1].Text, maxPreview)
		lastKind = string(entries[n-1].Kind)
	}
	f.mu.Lock()
	running := f.running
	f.mu.Unlock()
	return core.State{
		"running":        running,
		"count":          len(entries),
		"last":           last,
		"last_kind":      lastKind,
		"max_items":      cfg.MaxItems,
		"store_images":   cfg.StoreImages,
		"paste_on_copy":  cfg.PasteOnCopy,
		"retention_days": cfg.Retention,
	}
}

// OnHotkey implements core.Module: the hotkey opens the history viewer.
func (f *Feature) OnHotkey() error { return f.OpenUI() }

// OpenUI implements core.Module. On Windows it shows the native viewer, which
// runs on its own OS thread; other platforms report the history through the
// panel instead.
func (f *Feature) OpenUI() error {
	if f.ctx == nil {
		return errors.New("clipboard: 模块未初始化")
	}
	f.ctx.Bus.State(moduleID, f.State())
	return showViewer(f)
}

// ApplyOption implements core.Module: settings that do not need a restart take
// effect immediately (the panel restarts the module for Restart-flagged ones).
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return errors.New("clipboard: 模块未初始化")
	}
	switch key {
	case optMaxItems:
		n, ok := toInt(value)
		if !ok || n <= 0 {
			return fmt.Errorf("clipboard: %s 需要正整数", key)
		}
	case optRetentionDays:
		n, ok := toInt(value)
		if !ok || n <= 0 {
			return fmt.Errorf("clipboard: %s 需要正整数", key)
		}
	case optStoreImages, optPasteOnCopy:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("clipboard: %s 需要布尔值", key)
		}
	default:
		return fmt.Errorf("clipboard: 未知配置项 %s", key)
	}
	f.applyConfig()
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// RunAction executes a declared panel action.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case actionOpen:
		return f.OpenUI()
	case actionWriteLast:
		entries := f.hist.All()
		if len(entries) == 0 {
			return errors.New("clipboard: 暂无历史条目")
		}
		return f.writeBack(entries[len(entries)-1])
	case actionClear:
		f.hist.Clear()
		f.ctx.Logger.Info("已清空剪贴板历史", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
		return nil
	}
	return fmt.Errorf("clipboard: 未知动作 %s", id)
}

// applyConfig re-reads the persisted settings into the in-memory structures.
func (f *Feature) applyConfig() {
	cfg := f.snapshot()
	f.hist.Resize(cfg.MaxItems)
	f.hist.PruneOlder(retentionCutoff(cfg.Retention))
}

// snapshot reads this module's settings, falling back to the defaults.
func (f *Feature) snapshot() configView {
	cfg := configView{
		MaxItems:    defaultMaxItems,
		StoreImages: defaultStoreImages,
		PasteOnCopy: defaultPasteOnCopy,
		Retention:   defaultRetentionDays,
	}
	if f.ctx == nil {
		return cfg
	}
	cfg.Enabled = f.ctx.Config.Enabled()
	if n, ok := toInt(f.ctx.Config.Get(optMaxItems, defaultMaxItems)); ok && n > 0 {
		cfg.MaxItems = n
	}
	if v, ok := f.ctx.Config.Get(optStoreImages, defaultStoreImages).(bool); ok {
		cfg.StoreImages = v
	}
	if v, ok := f.ctx.Config.Get(optPasteOnCopy, defaultPasteOnCopy).(bool); ok {
		cfg.PasteOnCopy = v
	}
	if n, ok := toInt(f.ctx.Config.Get(optRetentionDays, defaultRetentionDays)); ok && n > 0 {
		cfg.Retention = n
	}
	return cfg
}

// watch records clipboard changes until ctx is cancelled.
func (f *Feature) watch(ctx context.Context, cfg configView) {
	var ch <-chan clip.Data
	if cfg.StoreImages {
		ch = clip.Watch(ctx, clip.FmtText, clip.FmtImage)
	} else {
		ch = clip.Watch(ctx, clip.FmtText)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-ch:
			if !ok {
				return
			}
			f.ingest(cfg, d)
		}
	}
}

// ingest stores one clipboard change, skipping empty payloads and the echo of
// this module's own write-back.
func (f *Feature) ingest(cfg configView, d clip.Data) {
	if len(d.Bytes) == 0 {
		return
	}
	if d.Format == clip.FmtImage {
		if !cfg.StoreImages || f.isEcho(clip.FmtImage, d.Bytes) {
			return
		}
		f.hist.AddImage(d.Bytes)
		f.ctx.Logger.Debug("已记录剪贴板图片", "module", moduleID, "bytes", len(d.Bytes))
		return
	}
	text := strings.TrimRight(string(d.Bytes), " \t\r\n")
	if text == "" || f.isEcho(clip.FmtText, []byte(text)) {
		return
	}
	f.hist.AddText(text)
	f.ctx.Logger.Debug("已记录剪贴板文本", "module", moduleID, "chars", len(text))
}

// writeBack writes an entry back and, when configured, pastes it.
func (f *Feature) writeBack(e Entry) error {
	return f.put(e, f.snapshot().PasteOnCopy)
}

// put writes an entry onto the system clipboard, optionally synthesising a
// paste into whatever window the user was last working in.
func (f *Feature) put(e Entry, autoPaste bool) error {
	if f.ctx == nil {
		return errors.New("clipboard: 模块未初始化")
	}
	format, buf := clip.FmtText, []byte(e.Text)
	if e.Kind == KindImage {
		format, buf = clip.FmtImage, e.Data
	}
	if len(buf) == 0 {
		return errors.New("clipboard: 条目内容为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if _, err := clip.Write(ctx, format, buf); err != nil {
		f.ctx.Logger.Error("写回剪贴板失败", "module", moduleID, "id", e.ID, "err", err)
		return err
	}
	f.markEcho(format, buf)

	if autoPaste {
		if err := sendPaste(); err != nil {
			// Pasting is a bonus: the entry is on the clipboard either way.
			f.ctx.Logger.Warn("自动粘贴未生效", "module", moduleID, "err", err)
			f.ctx.Bus.Log(moduleID, "warn", "自动粘贴未生效："+err.Error())
		}
	}
	f.ctx.Logger.Info("已写回剪贴板", "module", moduleID, "id", e.ID, "kind", e.Kind)
	f.ctx.Bus.Notice(moduleID, "已写回剪贴板")
	return nil
}

// markEcho records the payload just written so ingest can ignore it.
func (f *Feature) markEcho(format clip.Format, buf []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.echoAt = time.Now()
	if format == clip.FmtImage {
		f.echoText = ""
		f.echoImage = append([]byte(nil), buf...)
		return
	}
	f.echoImage = nil
	f.echoText = string(buf)
}

// isEcho reports whether buf is this module's own recent write-back.
func (f *Feature) isEcho(format clip.Format, buf []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.echoAt.IsZero() || time.Since(f.echoAt) > echoWindow {
		return false
	}
	if format == clip.FmtImage {
		return len(f.echoImage) > 0 && bytes.Equal(f.echoImage, buf)
	}
	return f.echoText != "" && f.echoText == string(buf)
}

// configView is this module's settings as read from the configuration.
type configView struct {
	Enabled     bool
	MaxItems    int
	StoreImages bool
	PasteOnCopy bool
	Retention   int
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

// retentionCutoff turns a retention-in-days option into a pruning deadline.
func retentionCutoff(days int) time.Time {
	if days <= 0 {
		days = defaultRetentionDays
	}
	return time.Now().AddDate(0, 0, -days)
}

// summary flattens text to a single-line excerpt for the panel.
func summary(s string, max int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
