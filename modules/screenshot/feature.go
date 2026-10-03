// Package screenshot implements a Snipaste-style screenshot tool: the global
// hotkey grabs the display, a region editor lets the user pick a rectangle, and
// the selection can then be saved to disk and/or copied to the clipboard.
package screenshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
	clip "golang.design/x/clipboard"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
)

// moduleID is the stable identifier used by the registry, the configuration
// file and hotkey bindings; it must match the directory name.
const moduleID = "screenshot"

// Option keys.
const (
	optFormat     = "format"
	optJPGQuality = "jpg_quality"
	optCopyAfter  = "copy_after"
	optSaveDir    = "save_dir"
	optMaxHistory = "max_history"
)

// Option defaults, kept in sync with internal/config.Default().
const (
	defaultFormat     = "png"
	defaultJPGQuality = 90
	defaultCopyAfter  = true
	defaultSaveDir    = ""
	defaultMaxHistory = 200
)

// Action ids surfaced in the preferences panel.
const (
	actionCapture = "capture"
	actionOpenDir = "open_dir"
)

// savedNamePrefix is the filename prefix of every saved screenshot; the
// startup cleaner uses it (plus a numeric stem and a known extension) to tell
// this module's own files apart from anything else in the directory.
const savedNamePrefix = "screenshot_"

// Timeouts for the operations that touch another process or the OS UI.
const (
	clipboardTimeout = 3 * time.Second
	shutdownTimeout  = 2 * time.Second
)

// errNotReady is returned when a method runs before Init.
var errNotReady = errors.New("screenshot: 模块未初始化")

// Feature implements the screenshot module.
type Feature struct {
	core.Base

	ctx *core.Context

	mu      sync.Mutex
	running bool
	busy    bool
	saved   []string
	wg      sync.WaitGroup
}

// NewFeature constructs the screenshot module.
func NewFeature() core.Module { return &Feature{} }

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "截图工具" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "区域截图后保存或复制到剪贴板（Snipaste 风格），默认热键 F1"
}

// Options implements core.Module.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optFormat, Label: "保存格式", Kind: core.KindSelect, Default: defaultFormat,
			Choices: []core.Choice{
				{Value: "png", Label: "PNG（无损）"},
				{Value: "jpg", Label: "JPG（体积小）"},
			}},
		{Key: optJPGQuality, Label: "JPG 质量", Kind: core.KindInt,
			Default: defaultJPGQuality, Min: 10, Max: 100, Step: 5,
			VisibleIf: &core.VisibleIf{Key: optFormat, Value: "jpg"},
			Help:      "仅对 JPG 生效"},
		{Key: optCopyAfter, Label: "截图后复制到剪贴板", Kind: core.KindBool, Default: defaultCopyAfter},
		{Key: optSaveDir, Label: "保存目录", Kind: core.KindString, Default: defaultSaveDir,
			Help: "留空则使用本模块的数据目录"},
		{Key: optMaxHistory, Label: "最多保留张数", Kind: core.KindInt,
			Default: defaultMaxHistory, Min: 10, Max: 2000, Step: 10,
			Help: "超出后自动删除最早的图片（含历史文件）"},
	}
}

// Actions implements core.Module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionCapture, Label: "立即截图", Kind: core.ActionNormal,
			Description: "截取主显示器并打开区域选择窗口"},
		{ID: actionOpenDir, Label: "打开保存目录", Kind: core.ActionOpen,
			Description: "在文件管理器中打开截图保存目录"},
	}
}

// Init implements core.Module. The save directory outlives the process, so
// Init adopts the files already on disk and trims past max_history; otherwise
// the directory would grow without bound across restarts.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errNotReady
	}
	f.ctx = ctx
	f.enforceHistoryLimit()
	return nil
}

// Start implements core.Module: nothing runs in the background, so it only
// announces readiness. The clipboard is opened lazily, on first use.
func (f *Feature) Start() error {
	if f.ctx == nil {
		return errNotReady
	}
	if !f.ctx.Config.Enabled() {
		return nil
	}

	f.mu.Lock()
	f.running = true
	f.mu.Unlock()

	f.ctx.Logger.Info("截图工具已就绪", "module", moduleID, "hotkey", f.ctx.Config.Hotkey())
	f.ctx.Bus.Log(moduleID, "info", "截图工具已就绪，按 "+f.ctx.Config.Hotkey()+" 开始截图")
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// Stop implements core.Module. It waits (briefly) for an in-flight capture and
// is safe to call repeatedly.
func (f *Feature) Stop() error {
	if f.ctx == nil {
		return nil
	}
	f.mu.Lock()
	wasRunning := f.running
	f.running = false
	f.mu.Unlock()

	if wasRunning {
		done := make(chan struct{})
		go func() { f.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(shutdownTimeout):
			f.ctx.Logger.Warn("等待截图任务结束超时", "module", moduleID)
		case <-f.ctx.Ctx.Done():
		}
		f.ctx.Logger.Info("截图工具已停止", "module", moduleID)
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// State implements core.Module: the panel-facing snapshot.
func (f *Feature) State() core.State {
	f.mu.Lock()
	running, busy := f.running, f.busy
	count := len(f.saved)
	last := ""
	if count > 0 {
		last = f.saved[count-1]
	}
	f.mu.Unlock()

	dir := f.saveDir()
	max := maxHistory(f.ctx)
	return core.State{
		"running":     running,
		"capturing":   busy,
		"saved_count": count,
		"last_saved":  last,
		"save_dir":    dir,
		"format":      f.format(),
		"max_history": max,
	}
}

// OnHotkey implements core.Module: it grabs the screen and opens the editor.
//
// The editor owns a modal window, so the capture runs on its own goroutine and
// the hotkey returns immediately rather than blocking the hotkey dispatcher.
func (f *Feature) OnHotkey() error {
	if f.ctx == nil {
		return errNotReady
	}
	if !f.claim() {
		f.ctx.Logger.Warn("上一次截图尚未结束，忽略本次请求", "module", moduleID)
		return nil
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer f.release()
		f.capture()
	}()
	return nil
}

// OpenUI implements core.Module: for this module it behaves like the hotkey.
func (f *Feature) OpenUI() error { return f.OnHotkey() }

// ApplyOption implements core.Module: every setting is read back from the
// configuration at use time, so this only validates and republishes state.
func (f *Feature) ApplyOption(key string, value any) error {
	if f.ctx == nil {
		return errNotReady
	}
	switch key {
	case optFormat:
		s, _ := value.(string)
		if s != "png" && s != "jpg" {
			return fmt.Errorf("screenshot: %s 只能是 png 或 jpg", key)
		}
	case optJPGQuality:
		n, ok := toInt(value)
		if !ok || n < 10 || n > 100 {
			return fmt.Errorf("screenshot: %s 需要 10-100 的整数", key)
		}
	case optCopyAfter:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("screenshot: %s 需要布尔值", key)
		}
	case optSaveDir:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("screenshot: %s 需要字符串", key)
		}
	case optMaxHistory:
		n, ok := toInt(value)
		if !ok || n < 1 {
			return fmt.Errorf("screenshot: %s 需要正整数", key)
		}
		// A shrunken limit must take effect immediately, not on the next save.
		f.enforceHistoryLimit()
	default:
		return fmt.Errorf("screenshot: 未知配置项 %s", key)
	}
	f.ctx.Bus.State(moduleID, f.State())
	return nil
}

// RunAction executes a declared panel action.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case actionCapture:
		return f.OnHotkey()
	case actionOpenDir:
		return f.openSaveDir()
	}
	return fmt.Errorf("screenshot: 未知动作 %s", id)
}

// capture takes the screenshot and hands it to the platform editor.
func (f *Feature) capture() {
	if err := clip.Init(); err != nil {
		f.ctx.Logger.Error("初始化剪贴板失败", "module", moduleID, "err", err)
		f.ctx.Bus.Log(moduleID, "error", "剪贴板不可用，将无法复制截图")
	}
	if screenshot.NumActiveDisplays() <= 0 {
		f.ctx.Logger.Warn("未找到可用的显示器", "module", moduleID)
		f.ctx.Bus.Notice(moduleID, "未找到可用的显示器")
		return
	}

	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		f.ctx.Logger.Error("截屏失败", "module", moduleID, "err", err)
		f.ctx.Bus.Log(moduleID, "error", "截屏失败："+err.Error())
		return
	}
	f.ctx.Bus.Progress(moduleID, actionCapture, 50, "已进入区域选择")

	openEditor(f.ctx, img, bounds, f.save, f.copyImage)
	f.ctx.Bus.Progress(moduleID, actionCapture, 100, "完成")
	f.ctx.Bus.State(moduleID, f.State())
}

// claim marks a capture as in progress, refusing overlapping ones.
func (f *Feature) claim() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return false
	}
	f.busy = true
	return true
}

// release clears the in-progress flag.
func (f *Feature) release() {
	f.mu.Lock()
	f.busy = false
	f.mu.Unlock()
	f.ctx.Bus.State(moduleID, f.State())
}

// save writes the cropped image into the configured directory.
func (f *Feature) save(img image.Image) {
	dir := f.saveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.ctx.Logger.Error("创建截图目录失败", "module", moduleID, "dir", dir, "err", err)
		f.ctx.Bus.Notice(moduleID, "无法创建截图目录："+dir)
		return
	}
	name := filepath.Join(dir, fmt.Sprintf(savedNamePrefix+"%d%s", time.Now().UnixMilli(), f.ext()))
	if err := f.writeImage(name, img); err != nil {
		f.ctx.Logger.Error("保存截图失败", "module", moduleID, "file", name, "err", err)
		f.ctx.Bus.Notice(moduleID, "保存截图失败："+err.Error())
		return
	}
	f.remember(name)
	f.ctx.Logger.Info("截图已保存", "module", moduleID, "file", name)
	f.ctx.Bus.Notice(moduleID, "已保存 "+name)
	f.ctx.Bus.State(moduleID, f.State())
}

// copyImage pushes the cropped image onto the clipboard as a PNG.
func (f *Feature) copyImage(img image.Image) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		f.ctx.Logger.Error("PNG 编码失败", "module", moduleID, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	if _, err := clip.Write(ctx, clip.FmtImage, buf.Bytes()); err != nil {
		f.ctx.Logger.Error("写入剪贴板失败", "module", moduleID, "err", err)
		f.ctx.Bus.Notice(moduleID, "复制到剪贴板失败")
		return
	}
	f.ctx.Logger.Info("截图已复制到剪贴板", "module", moduleID, "bytes", buf.Len())
	f.ctx.Bus.Notice(moduleID, "截图已复制到剪贴板")
}

// writeImage encodes img into path using the configured format.
func (f *Feature) writeImage(path string, img image.Image) error {
	fp, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fp.Close()

	if f.format() == "jpg" {
		return jpeg.Encode(fp, img, &jpeg.Options{Quality: f.quality()})
	}
	return png.Encode(fp, img)
}

// remember records a saved file and prunes older ones past the history limit.
func (f *Feature) remember(path string) {
	f.mu.Lock()
	f.saved = append(f.saved, path)
	var stale []string
	if max := maxHistory(f.ctx); max > 0 && len(f.saved) > max {
		stale = f.saved[:len(f.saved)-max]
		f.saved = f.saved[len(f.saved)-max:]
	}
	f.mu.Unlock()

	for _, old := range stale {
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			f.ctx.Logger.Warn("删除旧截图失败", "module", moduleID, "file", old, "err", err)
		}
	}
}

// isModuleScreenshot reports whether name follows this module's save pattern
// (screenshot_<digits>.png|.jpg). The startup cleaner only ever deletes files
// matching it, so user documents in the same directory are never touched.
func isModuleScreenshot(name string) bool {
	if !strings.HasPrefix(name, savedNamePrefix) {
		return false
	}
	ext := filepath.Ext(name)
	if ext != ".png" && ext != ".jpg" {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(name, savedNamePrefix), ext)
	if stem == "" {
		return false
	}
	for _, r := range stem {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// enforceHistoryLimit adopts the files this module already saved across
// restarts: it lists them oldest-first, deletes everything past max_history
// and repopulates f.saved with the survivors so in-session pruning continues
// from the real directory state. A missing or unreadable directory is silently
// ignored — there is simply nothing to clean.
func (f *Feature) enforceHistoryLimit() {
	dir := f.saveDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type shot struct {
		path string
		mod  time.Time
	}
	var shots []shot
	for _, e := range entries {
		if e.IsDir() || !isModuleScreenshot(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		shots = append(shots, shot{path: filepath.Join(dir, e.Name()), mod: info.ModTime()})
	}
	sort.Slice(shots, func(i, j int) bool {
		if shots[i].mod.Equal(shots[j].mod) {
			return shots[i].path < shots[j].path
		}
		return shots[i].mod.Before(shots[j].mod)
	})

	var stale []string
	if max := maxHistory(f.ctx); max > 0 && len(shots) > max {
		for _, s := range shots[:len(shots)-max] {
			stale = append(stale, s.path)
		}
		shots = shots[len(shots)-max:]
	}

	kept := make([]string, len(shots))
	for i, s := range shots {
		kept[i] = s.path
	}
	f.mu.Lock()
	f.saved = kept
	f.mu.Unlock()

	for _, old := range stale {
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			f.ctx.Logger.Warn("删除旧截图失败", "module", moduleID, "file", old, "err", err)
		}
	}
}

// openSaveDir reveals the screenshot folder with the platform file manager.
func (f *Feature) openSaveDir() error {
	dir := f.saveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("screenshot: 无法创建目录 %s: %w", dir, err)
	}
	// sysutil.OpenURL hands the path to the shell, which opens Explorer
	// (Windows) or xdg-open (Linux). It stays cgo-free by design.
	if err := sysutil.OpenURL(dir); err != nil {
		return fmt.Errorf("screenshot: 打开目录失败: %w", err)
	}
	f.ctx.Logger.Info("已打开截图目录", "module", moduleID, "dir", dir)
	return nil
}

// ext returns the file-name suffix for the configured format.
func (f *Feature) ext() string {
	if f.format() == "jpg" {
		return ".jpg"
	}
	return ".png"
}

// format returns the configured image format, defaulting to png.
func (f *Feature) format() string {
	if f.ctx == nil {
		return defaultFormat
	}
	s, ok := f.ctx.Config.Get(optFormat, defaultFormat).(string)
	if !ok || (s != "jpg" && s != "png") {
		return defaultFormat
	}
	return s
}

// quality returns the JPG quality in [10,100].
func (f *Feature) quality() int {
	if f.ctx == nil {
		return defaultJPGQuality
	}
	n, ok := toInt(f.ctx.Config.Get(optJPGQuality, defaultJPGQuality))
	if !ok || n < 10 || n > 100 {
		return defaultJPGQuality
	}
	return n
}

// saveDir returns the directory screenshots are written to: the configured one,
// else this module's data directory, else a folder under the user's Pictures.
func (f *Feature) saveDir() string {
	if f.ctx != nil {
		if dir, ok := f.ctx.Config.Get(optSaveDir, defaultSaveDir).(string); ok {
			if dir = strings.TrimSpace(dir); dir != "" {
				return dir
			}
		}
		if f.ctx.DataDir != "" {
			return f.ctx.DataDir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Pictures", "PCMannager")
	}
	return "."
}

// maxHistory reports how many screenshots to keep.
func maxHistory(ctx *core.Context) int {
	if ctx == nil {
		return defaultMaxHistory
	}
	n, ok := toInt(ctx.Config.Get(optMaxHistory, defaultMaxHistory))
	if !ok || n < 1 {
		return defaultMaxHistory
	}
	return n
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
