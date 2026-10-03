// Package clipboard implements the Ditto-style clipboard history module: it
// records what the user copies, keeps a bounded history and can write any
// entry back onto the system clipboard.
//
// Image payloads are never kept in memory: each captured PNG is flushed to a
// cache file under the module data directory and the history entry only holds
// its absolute path; write-back reads the file back on demand.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/winui"
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
	// cachePrefix/cacheExt shape the cache file names swept at startup.
	cachePrefix = "img_"
	cacheExt    = ".png"
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
	// skip it instead of re-adding it to the history. Only one kind is set at
	// a time; images/files are remembered by identity (a sampled fingerprint or
	// the cache path), never by bytes, so a write-back does not pin a BLOB in
	// memory either.
	echoText  string
	echoImage string
	echoFile  string
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
	return "Ditto 式剪贴板历史：自动记录复制的文本与图片（图片落盘缓存），可随时写回"
}

// Options implements core.Module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optMaxItems, Label: "历史条数上限", Kind: core.KindInt,
			Default: defaultMaxItems, Min: 10, Max: 5000, Step: 10,
			Help: "超出上限后丢弃最旧的未固定条目"},
		{Key: optStoreImages, Label: "记录图片", Kind: core.KindBool,
			Default: defaultStoreImages,
			Help:    "同时记录复制的图片（PNG 落盘缓存，不常驻内存）", Restart: true},
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
	// 条目被删除或驱逐时联动清理其缓存文件；失败仅记日志，不影响历史本身。
	f.hist.SetOnDelete(func(e Entry) {
		if e.Kind != KindImage || e.Path == "" {
			return // 文件条目的 Path 指向用户文件，绝不能删除
		}
		if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
			f.logCacheSweepFailure(e.Path, err)
		}
	})
	f.sweepCache()
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
	// golang.design/x/clipboard 的抽象只覆盖文本/图片位图，收不到资源管理器
	// 复制的文件列表；Windows 下由独立的 CF_HDROP 轮询器补齐这条通路。
	go f.pollFiles(ctx)

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
		last = summary(entrySummary(entries[n-1]), maxPreview)
		lastKind = string(entries[n-1].Kind)
	}
	f.mu.Lock()
	running := f.running
	f.mu.Unlock()
	return core.State{
		"running":         running,
		"count":           len(entries),
		"last":            last,
		"last_kind":       lastKind,
		"max_items":       cfg.MaxItems,
		"store_images":    cfg.StoreImages,
		"paste_on_copy":   cfg.PasteOnCopy,
		"retention_days":  cfg.Retention,
		"max_image_bytes": cfg.MaxImageBytes,
		"cache_dir":       f.cacheDir(),
		"cache_bytes":     f.cacheBytes(),
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
	// 显式 0/负数 = 不限制；键缺省时 Get 回落默认上限。
	if n, ok := toInt(f.ctx.Config.Get(optMaxImageBytes, defaultMaxImageBytes)); ok && n > 0 {
		cfg.MaxImageBytes = n
	} else if ok {
		cfg.MaxImageBytes = 0
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
			f.ingest(ctx, cfg, d)
		}
	}
}

// ingest stores one clipboard change, skipping empty payloads and the echo of
// this module's own write-back.
func (f *Feature) ingest(ctx context.Context, cfg configView, d clip.Data) {
	if len(d.Bytes) == 0 {
		return
	}
	if d.Format == clip.FmtImage {
		if !cfg.StoreImages || f.isEchoImage(d.Bytes) {
			return
		}
		// B3：单条图片字节上限。超过即拒收（不入历史，也不落盘），warn 上报
		// 面板；上限 ≤0 表示不限制（手改配置时）。图片最终只存磁盘缓存，
		// 但没有这道闸门时，一张超大 PNG 仍会先整块进内存。
		if cfg.MaxImageBytes > 0 && len(d.Bytes) > cfg.MaxImageBytes {
			f.ctx.Logger.Warn("剪贴板图片超过大小上限，已拒收", "module", moduleID,
				"bytes", len(d.Bytes), "limit", cfg.MaxImageBytes)
			return
		}
		path, size, err := f.writePNGCache(ctx, d.Bytes)
		if err != nil {
			// 落盘失败就放弃该条：宁可少记一条，也不把整张 PNG 留在内存里。
			f.ctx.Logger.Warn("图片缓存写入失败，放弃记录该条", "module", moduleID, "err", err)
			f.ctx.Bus.Log(moduleID, "warn", "图片缓存写入失败，未记录该条图片")
			return
		}
		f.hist.AddImageFile(path, size)
		f.ctx.Logger.Debug("已记录剪贴板图片", "module", moduleID, "bytes", size, "path", path)
		return
	}
	text := strings.TrimRight(string(d.Bytes), " \t\r\n")
	if text == "" || f.isEchoText(text) {
		return
	}
	f.hist.AddText(text)
	f.ctx.Logger.Debug("已记录剪贴板文本", "module", moduleID, "chars", len(text))
}

// writeBack writes an entry back and, when configured, pastes it.
//
// The paste target is the window that has focus right now, captured BEFORE the
// clipboard write: passing no target (winui.Invalid) made sendPaste reject the
// request outright, so the panel's "写回最近一条" could never auto-paste even
// with the option enabled.
func (f *Feature) writeBack(e Entry) error {
	autoPaste := f.snapshot().PasteOnCopy
	target := winui.HWND(0)
	if autoPaste {
		target = winui.FocusedWindow()
	}
	return f.put(e, autoPaste, target)
}

// put writes an entry onto the system clipboard, optionally synthesising a
// paste into target.
//
// target is the window that should receive the paste (the one the user was
// working in before our window took focus); it is ignored when autoPaste is
// false. The image branch reads the PNG back from its cache file on demand --
// the bytes never persist in the entry itself.
func (f *Feature) put(e Entry, autoPaste bool, target winui.HWND) error {
	if f.ctx == nil {
		return errors.New("clipboard: 模块未初始化")
	}
	switch e.Kind {
	case KindImage:
		png, err := os.ReadFile(e.Path)
		if err != nil {
			err = fmt.Errorf("clipboard: 读取图片缓存失败: %w", err)
			f.ctx.Logger.Error("写回图片失败", "module", moduleID, "id", e.ID, "path", e.Path, "err", err)
			return err
		}
		return f.writeBytes(e, clip.FmtImage, png, autoPaste, target)
	case KindFile:
		// CF_HDROP：聊天客户端与资源管理器把这类粘贴当作“发送文件”。
		// 非 Windows 平台 winui 返回 errUnsupported，错误照常上报。
		if err := winui.ClipboardFileDrop([]string{e.Path}); err != nil {
			f.ctx.Logger.Error("写回文件失败", "module", moduleID, "id", e.ID, "path", e.Path, "err", err)
			return err
		}
		f.markEchoFile(e.Path)
		f.afterWrite(e, autoPaste, target)
		return nil
	default:
		return f.writeBytes(e, clip.FmtText, []byte(e.Text), autoPaste, target)
	}
}

// writeBytes performs the actual clip.Write for text/image payloads and
// records the echo so the watcher does not re-ingest our own write.
func (f *Feature) writeBytes(e Entry, format clip.Format, buf []byte, autoPaste bool, target winui.HWND) error {
	if len(buf) == 0 {
		return errors.New("clipboard: 条目内容为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if _, err := clip.Write(ctx, format, buf); err != nil {
		f.ctx.Logger.Error("写回剪贴板失败", "module", moduleID, "id", e.ID, "err", err)
		return err
	}
	f.markEchoBytes(format, buf)
	f.afterWrite(e, autoPaste, target)
	return nil
}

// afterWrite handles the shared post-write bookkeeping: best-effort auto
// paste plus the log/notice every branch reports.
func (f *Feature) afterWrite(e Entry, autoPaste bool, target winui.HWND) {
	if autoPaste {
		if err := sendPaste(target); err != nil {
			// Pasting is a bonus: the entry is on the clipboard either way.
			f.ctx.Logger.Warn("自动粘贴未生效", "module", moduleID, "err", err)
			f.ctx.Bus.Log(moduleID, "warn", "自动粘贴未生效，请手动 Ctrl+V："+err.Error())
		}
	}
	f.ctx.Logger.Info("已写回剪贴板", "module", moduleID, "id", e.ID, "kind", e.Kind)
	f.ctx.Bus.Notice(moduleID, "已写回剪贴板")
}

// markEchoBytes records the payload just written so ingest can ignore it.
func (f *Feature) markEchoBytes(format clip.Format, buf []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.echoAt = time.Now()
	if format == clip.FmtImage {
		// 只记录身份（大小 + 采样指纹），不复制字节：写回大图不应把 PNG 钉在内存里。
		f.echoImage = imageFingerprint(buf)
		f.echoText = ""
		f.echoFile = ""
		return
	}
	f.echoText = string(buf)
	f.echoImage = ""
	f.echoFile = ""
}

// markEchoFile records the file path just written so the poller can ignore it.
func (f *Feature) markEchoFile(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.echoAt = time.Now()
	f.echoFile = path
	f.echoText = ""
	f.echoImage = ""
}

// isEchoText reports whether text is this module's own recent write-back.
func (f *Feature) isEchoText(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.echoAt.IsZero() || time.Since(f.echoAt) > echoWindow {
		return false
	}
	return f.echoText != "" && f.echoText == text
}

// isEchoImage reports whether png is this module's own recent write-back,
// compared by fingerprint so the full payload never needs to be stored.
func (f *Feature) isEchoImage(png []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.echoAt.IsZero() || time.Since(f.echoAt) > echoWindow {
		return false
	}
	return f.echoImage != "" && f.echoImage == imageFingerprint(png)
}

// isEchoFile reports whether path is this module's own recent write-back.
func (f *Feature) isEchoFile(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.echoAt.IsZero() || time.Since(f.echoAt) > echoWindow {
		return false
	}
	return f.echoFile != "" && f.echoFile == path
}

// imageFingerprint derives a cheap identity for a PNG payload: its size plus
// a sampled hash of head/middle/tail bytes. Only used to recognise our own
// echo within a 3s window, where a collision is practically impossible and
// benign (one duplicate entry at worst).
func imageFingerprint(buf []byte) string {
	if len(buf) == 0 {
		return ""
	}
	const samples = 32
	var b strings.Builder
	fmt.Fprintf(&b, "%d:", len(buf))
	for _, off := range []int{0, len(buf) / 2, len(buf) - samples} {
		if off < 0 {
			off = 0
		}
		end := off + samples
		if end > len(buf) {
			end = len(buf)
		}
		b.Write(buf[off:end])
	}
	return b.String()
}

// entrySummary renders one entry for the panel's "last" preview.
func entrySummary(e Entry) string {
	switch e.Kind {
	case KindImage:
		return fmt.Sprintf("[图片 %d KB]", e.Size/1024)
	case KindFile:
		return "[文件] " + e.Text
	}
	return e.Text
}

// cacheDir returns the directory holding image cache files (the module data
// directory itself; DataDir is per-module, so no extra subdirectory needed).
func (f *Feature) cacheDir() string {
	if f.ctx == nil || f.ctx.DataDir == "" {
		return ""
	}
	return f.ctx.DataDir
}

// writePNGCache flushes a captured PNG into the cache directory and returns
// its absolute path and byte size. The 3s echo window makes second-level
// timestamps unique enough; the fetch-based sweep still tolerates collisions.
func (f *Feature) writePNGCache(ctx context.Context, png []byte) (string, int, error) {
	dir := f.cacheDir()
	if dir == "" {
		return "", 0, errors.New("clipboard: 缓存目录不可用")
	}
	name := fmt.Sprintf("%s%d%s", cachePrefix, time.Now().Unix(), cacheExt)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o600); err != nil {
		return "", 0, err
	}
	return path, len(png), nil
}

// sweepCache removes cache files that no entry references anymore (crash
// leftovers, pruned entries whose cleanup was interrupted, name collisions).
// Real file entries live outside the cache dir and are never touched.
func (f *Feature) sweepCache() {
	dir := f.cacheDir()
	if dir == "" {
		return
	}
	keep := make(map[string]bool)
	for _, e := range f.hist.All() {
		if e.Path != "" {
			keep[e.Path] = true
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, cachePrefix+"*"+cacheExt))
	if err != nil {
		return
	}
	for _, p := range matches {
		if keep[p] {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			f.logCacheSweepFailure(p, err)
		}
	}
}

// logCacheSweepFailure reports a failed cache-file removal once per file.
func (f *Feature) logCacheSweepFailure(path string, err error) {
	f.ctx.Logger.Warn("剪贴板缓存文件删除失败", "module", moduleID, "path", path, "err", err)
}

// cacheBytes walks the cache directory and sums file sizes; on error it
// reports whatever was counted so far (the panel display is best-effort).
func (f *Feature) cacheBytes() int64 {
	dir := f.cacheDir()
	if dir == "" {
		return 0
	}
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		if info, err := de.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// configView is this module's settings as read from the configuration.
type configView struct {
	Enabled       bool
	MaxItems      int
	StoreImages   bool
	PasteOnCopy   bool
	Retention     int
	MaxImageBytes int // <=0 = 不限制
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
