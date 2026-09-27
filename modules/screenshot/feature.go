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
	optRecFPS     = "record_fps"
	optScrollWait = "scroll_interval_ms"
)

// Option defaults, kept in sync with internal/config.Default().
const (
	defaultFormat     = "png"
	defaultJPGQuality = 90
	defaultCopyAfter  = true
	defaultSaveDir    = ""
	defaultMaxHistory = 200
	defaultRecFPS     = recDefaultFPS
	// defaultScrollWait mirrors scrollInterval, in milliseconds.
	defaultScrollWait = 320
)

// Action ids surfaced in the preferences panel.
const (
	actionCapture  = "capture"
	actionOpenDir  = "open_dir"
	actionRecord   = "record"
	actionScroller = "scroll_capture"
)

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

	// rec is the in-flight recording, nil when not recording. It is held here so
	// a recording survives the editor window that started it (the user closes the
	// selection UI and the capture continues).
	rec *recorder

	// scroll is the in-flight scrolling capture; like rec it outlives the editor
	// that started it, because the user drives the scrolling after the region is
	// chosen and the window is parked away.
	scroll *scrollCapture
}

// NewFeature constructs the screenshot module.
func NewFeature() core.Module { return &Feature{} }

// ID implements core.Module.
func (f *Feature) ID() string { return moduleID }

// Name implements core.Module.
func (f *Feature) Name() string { return "截图工具" }

// Description implements core.Module.
func (f *Feature) Description() string {
	return "区域截图与标注、GIF 录屏、滚动长截图，默认热键 F1"
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
			Help: "超出后删除本次会话中最早保存的图片"},
		{Key: optRecFPS, Label: "录屏帧率 (fps)", Kind: core.KindInt,
			Default: defaultRecFPS, Min: recMinFPS, Max: recMaxFPS, Step: 1,
			Help: "GIF 录屏帧率；越高越流畅，文件也越大"},
		{Key: optScrollWait, Label: "滚动截图采样间隔 (ms)", Kind: core.KindInt,
			Default: defaultScrollWait, Min: 120, Max: 2000, Step: 20,
			Help: "拼接两帧之间的等待时间；页面滚动较慢时调大可提高成功率"},
	}
}

// Actions implements core.Module.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionCapture, Label: "立即截图", Kind: core.ActionNormal,
			Description: "截取主显示器并打开区域选择窗口"},
		{ID: actionRecord, Label: "录屏 (GIF)", Kind: core.ActionNormal,
			Description: "框选区域后录制为 GIF 动图"},
		{ID: actionScroller, Label: "滚动截图", Kind: core.ActionNormal,
			Description: "框选滚动区域，拼接为一张长截图"},
		{ID: actionOpenDir, Label: "打开保存目录", Kind: core.ActionOpen,
			Description: "在文件管理器中打开截图保存目录"},
	}
}

// Init implements core.Module.
func (f *Feature) Init(ctx *core.Context) error {
	if ctx == nil {
		return errNotReady
	}
	f.ctx = ctx
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
		// A recording or scrolling capture holds an OS-level loop; cancelling it
		// here is what stops the sampling rather than letting it run until the
		// process dies. Both are idempotent, so a capture that already finished on
		// its own is unaffected.
		if sc := f.activeScroller(); sc != nil {
			sc.requestStop()
		}
		if rec := f.activeRecorder(); rec != nil {
			rec.discard()
		}
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

	rec := f.activeRecorder()
	sc := f.activeScroller()

	dir := f.saveDir()
	max := maxHistory(f.ctx)
	st := core.State{
		"running":     running,
		"capturing":   busy,
		"saved_count": count,
		"last_saved":  last,
		"save_dir":    dir,
		"format":      f.format(),
		"max_history": max,
		"record_fps":  f.recordingFPS(),
	}
	// Reporting the in-flight jobs is what lets the panel offer a stop button and
	// show elapsed progress; a bare boolean would leave the user guessing.
	if rec != nil {
		st["recording"] = true
		st["rec_frames"] = rec.frameCount()
		st["rec_frames_cap"] = recMaxFrames
	}
	if sc != nil {
		st["scrolling"] = true
		st["scroll_height"] = sc.height()
	}
	return st
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
	case optRecFPS:
		n, ok := toInt(value)
		if !ok || n < recMinFPS || n > recMaxFPS {
			return fmt.Errorf("screenshot: %s 需要 %d-%d 的整数", key, recMinFPS, recMaxFPS)
		}
	case optScrollWait:
		n, ok := toInt(value)
		if !ok || n < 120 || n > 2000 {
			return fmt.Errorf("screenshot: %s 需要 120-2000 的整数", key)
		}
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
	case actionRecord:
		return f.openRegionEditor(edModeRecord)
	case actionScroller:
		return f.openRegionEditor(edModeScroll)
	case actionOpenDir:
		return f.openSaveDir()
	}
	return fmt.Errorf("screenshot: 未知动作 %s", id)
}

// openRegionEditor grabs the screen and opens the editor in the given mode.
//
// It mirrors OnHotkey's claim/release bookkeeping so a recording editor and a
// plain capture cannot overlap.
func (f *Feature) openRegionEditor(mode edMode) error {
	if !f.claim() {
		return fmt.Errorf("screenshot: 上一次操作尚未结束")
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer f.release()
		f.captureAs(mode)
	}()
	return nil
}

// capture takes the screenshot and hands it to the platform editor.
func (f *Feature) capture() { f.captureAs(edModeCapture) }

// captureAs grabs the primary display and opens the editor in mode.
func (f *Feature) captureAs(mode edMode) {
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

	hooks := editorHooks{
		Save:        f.save,
		Copy:        f.copyImage,
		StartRecord: f.startRecording,
		StartScroll: f.startScrolling,
		Stop:        f.stopCapture,
		Abort:       f.abortCapture,
		Status:      f.captureStatus,
		CopyResult:  f.copyRecordingFile,
		Reveal:      f.revealDir,
	}
	openEditor(f.ctx, img, bounds, mode, hooks)
	f.ctx.Bus.Progress(moduleID, actionCapture, 100, "完成")
	f.ctx.Bus.State(moduleID, f.State())
}

// startRecording begins a GIF recording of the editor's chosen region.
//
// The region is in SCREEN coordinates (the editor window spans the display), and
// beginRecording converts nothing: CaptureRect is a screen-space API.
func (f *Feature) startRecording(region image.Rectangle) error {
	_, err := f.beginRecording(region, f.recordingFPS())
	return err
}

// startScrolling begins a scrolling capture of the editor's chosen region.
func (f *Feature) startScrolling(region image.Rectangle, auto bool) error {
	_, err := f.beginScrollCapture(region, auto)
	return err
}

// recordCapFrames caps how long a captured frame is buffered before the take is
// declared over; see captureStatus.
const recWarnFrames = recMaxFrames - 3

// captureStatus reports the in-flight capture's progress and whether it stopped
// on its own, which is how the editor learns to collect the result without the
// user pressing anything.
//
// The "done" conditions are the two ways a capture ends without a stop request:
// the recording hit its frame cap, or the scroller reached the bottom of the
// page (or gave up).
func (f *Feature) captureStatus() (string, bool) {
	f.mu.Lock()
	rec, sc := f.rec, f.scroll
	f.mu.Unlock()

	if rec != nil {
		n := rec.frameCount()
		secs := 0
		if fps := rec.fps; fps > 0 {
			secs = n / fps
		}
		// Near the cap the sampling loop stops adding frames on its own, so the
		// take must be collected here.
		return fmt.Sprintf("录屏中 %d:%02d（%d 帧）", secs/60, secs%60, n), n >= recWarnFrames
	}
	if sc != nil {
		if sc.stopRequested() {
			return "正在收尾…", false
		}
		if reason := sc.stopReason(); reason != "" {
			return reason, true
		}
		return fmt.Sprintf("滚动截图中，已拼接 %d 像素", sc.height()), false
	}
	return "", false
}

// stopCapture ends the in-flight capture, saves the result and reports what was
// produced.
//
// It is the single finalisation path for both a user-pressed stop and a capture
// that ended on its own; the recorder/scroller are marked finished, so a second
// call is a no-op.
func (f *Feature) stopCapture() string {
	f.mu.Lock()
	rec, sc := f.rec, f.scroll
	f.mu.Unlock()

	if rec != nil {
		data, err := rec.finish()
		if err != nil {
			f.ctx.Logger.Warn("录屏结束但没有可用内容", "module", moduleID, "err", err)
			sharedOutcome.set("录屏失败："+err.Error(), "")
			return "录屏失败：" + err.Error()
		}
		path, err := f.saveRecording(data)
		if err != nil {
			sharedOutcome.set("保存录屏失败："+err.Error(), "")
			return "保存录屏失败：" + err.Error()
		}
		text := fmt.Sprintf("录屏已保存 %s（%d 帧，%.1f MB）",
			filepath.Base(path), rec.frameCount(), float64(len(data))/(1024*1024))
		sharedOutcome.set(text, path)
		return text
	}

	if sc != nil {
		sc.requestStop()
		img := sc.image()
		f.forgetScroller(sc)
		if img == nil {
			sharedOutcome.set("滚动截图没有产生内容", "")
			return "滚动截图没有产生内容"
		}
		path, err := f.saveScrolled(img)
		if err != nil {
			sharedOutcome.set("保存滚动截图失败："+err.Error(), "")
			return "保存滚动截图失败：" + err.Error()
		}
		text := fmt.Sprintf("滚动截图已保存 %s（%d×%d）",
			filepath.Base(path), img.Bounds().Dx(), img.Bounds().Dy())
		sharedOutcome.set(text, path)
		return text
	}
	return ""
}

// abortCapture discards the in-flight capture without producing a file.
func (f *Feature) abortCapture() {
	f.mu.Lock()
	rec, sc := f.rec, f.scroll
	f.mu.Unlock()

	if rec != nil {
		rec.discard()
		f.ctx.Bus.Notice(moduleID, "已丢弃本次录屏")
	}
	if sc != nil {
		sc.requestStop()
		f.forgetScroller(sc)
		f.ctx.Bus.Notice(moduleID, "已取消滚动截图")
	}
}

// copyRecordingFile puts an already saved capture file on the clipboard.
func (f *Feature) copyRecordingFile(path string) error {
	if err := copyFileToClipboard(path); err != nil {
		f.ctx.Logger.Warn("复制文件到剪贴板失败", "module", moduleID, "file", path, "err", err)
		return err
	}
	f.ctx.Bus.Notice(moduleID, "已复制到剪贴板")
	return nil
}

// revealDir opens a folder in the platform file manager.
func (f *Feature) revealDir(dir string) {
	if err := sysutil.OpenURL(dir); err != nil {
		f.ctx.Logger.Warn("打开目录失败", "module", moduleID, "dir", dir, "err", err)
	}
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
	path, err := f.writeShot(img)
	if err != nil {
		f.ctx.Logger.Error("保存截图失败", "module", moduleID, "err", err)
		f.ctx.Bus.Notice(moduleID, "保存截图失败："+err.Error())
		return
	}
	f.ctx.Bus.Notice(moduleID, "已保存 "+path)
}

// saveScrolled writes a stitched long shot and returns its path.
func (f *Feature) saveScrolled(img image.Image) (string, error) {
	path, err := f.writeShotNamed(img, "longshot")
	if err != nil {
		f.ctx.Logger.Error("保存滚动截图失败", "module", moduleID, "err", err)
		return "", err
	}
	// The result is announced through the editor's result bar, so only the log
	// records it here; a Notice would race the bar and the panel toast.
	f.ctx.Logger.Info("滚动截图已保存", "module", moduleID, "file", path)
	return path, nil
}

// writeShot encodes img as a plain screenshot: timestamped name, history pruned.
func (f *Feature) writeShot(img image.Image) (string, error) {
	return f.writeShotNamed(img, "screenshot")
}

// writeShotNamed writes img into the configured directory. prefix names the
// file (screenshot / longshot), so the two workflows are distinguishable on
// disk.
func (f *Feature) writeShotNamed(img image.Image, prefix string) (string, error) {
	dir := f.saveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.ctx.Logger.Error("创建截图目录失败", "module", moduleID, "dir", dir, "err", err)
		f.ctx.Bus.Notice(moduleID, "无法创建截图目录："+dir)
		return "", err
	}
	name := filepath.Join(dir, fmt.Sprintf("%s_%d%s", prefix, time.Now().UnixMilli(), f.ext()))
	if err := f.writeImage(name, img); err != nil {
		return "", err
	}
	f.remember(name)
	f.ctx.Logger.Info("截图已保存", "module", moduleID, "file", name)
	f.ctx.Bus.State(moduleID, f.State())
	return name, nil
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
