// Package updater — module wiring: options, actions, periodic check loop,
// download staging and the apply-update flow.
package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/sysutil"
)

// moduleID must stay stable: it keys config, hotkey bindings and the panel.
const moduleID = "updater"

// stagedName is the file the downloaded update is saved as, inside DataDir.
const stagedName = "pcmannager.update"

// Option keys.
const (
	optAutoCheck  = "auto_check"
	optIntervalH  = "interval_hours"
	optIncludePre = "include_prerelease"
	optNotify     = "notify"
)

// Action IDs.
const (
	actionCheck    = "check_now"
	actionDownload = "download"
	actionInstall  = "apply_update"
)

// Feature implements core.Module for the updater.
type Feature struct {
	core.Base
	ctx *core.Context

	mu       sync.Mutex
	checking bool
	// busy marks an in-flight download/apply so panel buttons can't
	// double-trigger the same work.
	busy bool
	// staged is the path of a downloaded-but-not-yet-applied update.
	staged string
	// last is the most recent check result surfaced in the panel state.
	last CheckResult
}

// CheckResult is the panel-facing snapshot of the most recent check/download.
type CheckResult struct {
	// CheckedAt is when the last check ran (RFC3339), "" if never.
	CheckedAt string `json:"checked_at"`
	// Current is the running version.
	Current string `json:"current"`
	// Latest is the newest release tag seen, "" if unknown.
	Latest string `json:"latest"`
	// UpdateAvailable is true when Latest is newer than Current.
	UpdateAvailable bool `json:"update_available"`
	// Asset is the matched platform asset name, "" if none.
	Asset string `json:"asset"`
	// Staged is the path of the downloaded update package, "" if none.
	Staged string `json:"staged"`
	// Err is the last error message, "" if healthy.
	Err string `json:"err"`
	// URL is the release page (manual fallback).
	URL string `json:"url"`
}

// NewFeature constructs the updater module.
func NewFeature() *Feature { return &Feature{} }

func (f *Feature) ID() string   { return moduleID }
func (f *Feature) Name() string { return "自动更新" }
func (f *Feature) Description() string {
	return "自动检查 GitHub Releases 新版本；可下载暂存更新包，应用更新需在面板显式触发（需管理员确认）"
}

// Options declares the module settings.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optAutoCheck, Label: "自动检查更新", Kind: core.KindBool, Default: true,
			Help: "按固定间隔检查 GitHub Releases；关闭后仅可手动检查"},
		{Key: optIntervalH, Label: "检查间隔（小时）", Kind: core.KindInt, Default: 24, Min: 1, Max: 168, Step: 1,
			Help: "两次自动检查之间的间隔，1-168 小时"},
		{Key: optIncludePre, Label: "包含预发布版本", Kind: core.KindBool, Default: false,
			Help: "是否将 rc/预发布 tag 视为可用更新"},
		{Key: optNotify, Label: "发现新版本时弹系统通知", Kind: core.KindBool, Default: true,
			Help: "同时写入面板事件日志；关闭后仅面板可见"},
	}
}

// Actions declares the panel buttons.
func (f *Feature) Actions() []core.Action {
	return []core.Action{
		{ID: actionCheck, Label: "检查更新", Group: "更新", Kind: core.ActionNormal,
			Description: "立即查询 GitHub Releases 并比对当前版本"},
		{ID: actionDownload, Label: "下载更新包", Group: "更新", Kind: core.ActionNormal,
			Description: "下载与当前平台匹配的更新包到本地暂存（不自动应用）"},
		{ID: actionInstall, Label: "应用已下载的更新", Group: "更新", Kind: core.ActionDanger, Confirm: true, Admin: true,
			Description: "用暂存的更新包替换当前程序；需管理员确认，完成后自动重启生效"},
	}
}

// Init stores the context, seeds state and starts the periodic check loop.
func (f *Feature) Init(ctx *core.Context) error {
	f.ctx = ctx
	f.last.Current = f.currentVersion()
	// A stale staged file from a previous run is still applicable.
	if p := filepath.Join(ctx.DataDir, stagedName); fileExists(p) {
		f.staged = p
	}
	if boolOpt(f.ctx.Config.Get(optAutoCheck, true)) {
		go f.loop()
	}
	return nil
}

// Start implements core.Module: it publishes the initial state so the panel
// shows the module as running with the detected version. The periodic check
// loop was already started in Init.
func (f *Feature) Start() error {
	f.last.Current = f.currentVersion()
	if f.ctx != nil {
		f.ctx.Bus.State(moduleID, f.State())
	}
	return nil
}

// Stop implements core.Module. The check loop exits via ctx cancellation;
// nothing to stop here. A staged download is intentionally kept: the user may
// apply it after restarting into this or a later session.
func (f *Feature) Stop() error { return nil }

// loop runs the periodic check until app shutdown.
func (f *Feature) loop() {
	// First check shortly after boot so a fresh release is noticed promptly;
	// later checks follow the configured interval.
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-f.ctx.Ctx.Done():
			return
		case <-t.C:
			if boolOpt(f.ctx.Config.Get(optAutoCheck, true)) {
				if _, err := f.Check(f.ctx.Ctx); err != nil {
					f.ctx.Logger.Warn("自动检查更新失败", "err", err)
				}
			}
			t.Reset(f.interval())
		}
	}
}

// interval reads the configured check interval.
func (f *Feature) interval() time.Duration {
	h, ok := f.ctx.Config.Get(optIntervalH, 24).(int)
	if !ok || h <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(h) * time.Hour
}

// currentVersion reports the running version. It uses the same fallback chain
// as internal/app.Version (ldflags stamp → build info → dev), but reading the
// app package directly would invert the modules→app dependency, so the logic
// is duplicated here deliberately and MUST stay in sync with version.go.
func (f *Feature) currentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "0.0.0-dev"
}

// Check queries GitHub for the latest release and updates the module state.
// Safe for concurrent calls; concurrent checks collapse into one.
func (f *Feature) Check(ctx context.Context) (CheckResult, error) {
	f.mu.Lock()
	if f.checking {
		f.mu.Unlock()
		return f.last, fmt.Errorf("检查已在进行中")
	}
	f.checking = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.checking = false
		f.mu.Unlock()
	}()

	rel, err := fetchLatest(ctx, newHTTPClient())
	res := f.recordCheck(rel, err)
	return res, err
}

// recordCheck merges a check round-trip into the module state. Call without
// holding f.mu (it locks internally).
func (f *Feature) recordCheck(rel *Release, err error) CheckResult {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err != nil {
		f.last.Err = err.Error()
		f.last.CheckedAt = time.Now().Format(time.RFC3339)
		if f.ctx != nil {
			f.ctx.Bus.Log(moduleID, "error", "检查更新失败: "+err.Error())
		}
		return f.last
	}

	f.last.Err = ""
	f.last.CheckedAt = time.Now().Format(time.RFC3339)
	f.last.Latest = rel.TagName
	f.last.URL = rel.HTMLURL
	f.last.UpdateAvailable = f.isNewer(rel.TagName)
	if a := rel.findAsset(); a != nil {
		f.last.Asset = a.Name
	}

	if f.ctx != nil {
		switch {
		case f.last.UpdateAvailable:
			f.ctx.Bus.Log(moduleID, "info", "发现新版本 "+rel.TagName+"（当前 "+f.last.Current+"）")
			if boolOpt(f.ctx.Config.Get(optNotify, true)) {
				_ = sysutil.Notify("PCMannager", "发现新版本 "+rel.TagName+"，可到面板更新")
			}
		default:
			f.ctx.Bus.Log(moduleID, "info", "已是最新版本 "+f.last.Current)
		}
	}
	return f.last
}

// isNewer reports whether tag is newer than the running version. Both are
// expected in the vMAJOR.MINOR.PATCH[-PRERELEASE] form; anything unparsable
// compares equal (no update), which fails closed.
func (f *Feature) isNewer(tag string) bool {
	cur := parseSemver(f.last.Current)
	// "0.0.0-dev" is the unstamped-build placeholder, not a real pre-release:
	// treat it as a final (pre-release-less) version so pre-release tags keep
	// requiring the opt-in.
	if cur != nil && cur.Prerelease == "dev" {
		cur.Prerelease = ""
	}
	next := parseSemver(tag)
	if next == nil {
		return false
	}
	if cur == nil {
		// Unknown running version (dev builds): treat any release as newer so
		// dev builds can still update, but pre-releases stay opt-in.
		return f.includePre() || parseSemver(tag).Prerelease == ""
	}
	if next.LessThan(cur) {
		return false
	}
	// Equal versions with different pre-release: newer only if the tag has a
	// pre-release and the running one does not? No — an rc is OLDER than its
	// final release. SemVer: 1.0.0-rc1 < 1.0.0. So when versions differ only
	// in pre-release, "newer" means the tag's pre-release ranks higher.
	if cur.Major == next.Major && cur.Minor == next.Minor && cur.Patch == next.Patch {
		if cur.Prerelease == next.Prerelease {
			return false
		}
		if cur.Prerelease == "" {
			return false // running final; any rc for the same version is older
		}
		if next.Prerelease == "" {
			return true // running rc; the final release is newer
		}
		return comparePrerelease(next.Prerelease, cur.Prerelease) > 0 && f.includePre()
	}
	// Numeric triple differs. A pre-release tag still requires the opt-in
	// when the running version is a final release (e.g. 0.0.0-dev → v0.2.0-rc1
	// must not surface unless the user asked for pre-releases).
	if next.Prerelease != "" && cur.Prerelease == "" {
		return f.includePre()
	}
	return true
}

// includePre reads the include_prerelease option.
func (f *Feature) includePre() bool {
	if f.ctx == nil {
		return false
	}
	on, _ := f.ctx.Config.Get(optIncludePre, false).(bool)
	return on
}

// RunAction dispatches panel buttons.
func (f *Feature) RunAction(id string, params map[string]string) error {
	switch id {
	case actionCheck:
		_, err := f.Check(context.Background())
		return err
	case actionDownload:
		return f.Download(context.Background())
	case actionInstall:
		return f.Apply()
	default:
		return fmt.Errorf("未知动作 %q", id)
	}
}

// Download fetches the platform-matching asset of the latest release into
// DataDir/pcmannager.update. It refuses to download when no newer version is
// known or none of the release assets matches this platform.
func (f *Feature) Download(ctx context.Context) error {
	f.mu.Lock()
	if f.busy {
		f.mu.Unlock()
		return fmt.Errorf("已有更新操作在进行中")
	}
	f.busy = true
	last := f.last
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.busy = false
		f.mu.Unlock()
	}()

	if last.Latest == "" {
		return fmt.Errorf("请先检查更新")
	}
	if !last.UpdateAvailable {
		return fmt.Errorf("当前已是最新版本 %s，无需下载", last.Current)
	}
	if last.Asset == "" {
		return fmt.Errorf("最新版本没有匹配当前平台 %s/%s 的更新包", runtime.GOOS, runtime.GOARCH)
	}

	// Resolve the download URL fresh: the check step only stores the name.
	hc := newHTTPClient()
	rel, err := fetchLatest(ctx, hc)
	if err != nil {
		return err
	}
	a := rel.findAsset()
	if a == nil {
		return fmt.Errorf("最新版本没有匹配当前平台 %s/%s 的更新包", runtime.GOOS, runtime.GOARCH)
	}

	dst := filepath.Join(f.ctx.DataDir, stagedName)
	f.ctx.Bus.Log(moduleID, "info", "开始下载更新包 "+a.Name)
	n, sum, err := download(ctx, hc, a.BrowserDownloadURL, dst)
	if err != nil {
		f.ctx.Bus.Log(moduleID, "error", "下载失败: "+err.Error())
		return err
	}
	f.mu.Lock()
	f.staged = dst
	f.last.Staged = dst
	f.mu.Unlock()
	f.ctx.Bus.Log(moduleID, "info", fmt.Sprintf("更新包已下载 (%s, %d 字节)，可在面板确认后应用", sum[:12], n))
	return nil
}

// Apply replaces the running executable with the staged update. The actual
// file swap must happen after this process exits (Windows locks the running
// image), so it is performed by an elevated helper that waits for us.
func (f *Feature) Apply() error {
	f.mu.Lock()
	staged := f.staged
	f.mu.Unlock()

	if staged == "" {
		return fmt.Errorf("没有已下载的更新包，请先下载")
	}
	if _, err := os.Stat(staged); err != nil {
		return fmt.Errorf("暂存文件不可用: %w", err)
	}

	if runtime.GOOS == "windows" {
		return f.applyWindows(staged)
	}
	return f.applyUnix(staged)
}

// applyWindows writes a PowerShell helper that waits for this process to
// exit, backs up the current exe, swaps in the staged update and restarts.
// The helper is launched elevated via RunElevated (cmd.exe /c powershell
// -File), which sidesteps nested quoting of an inline -Command.
func (f *Feature) applyWindows(staged string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		exePath = exe
	}
	script := filepath.Join(f.ctx.DataDir, "apply_update.ps1")
	body := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$src = '%s'
$dst = '%s'
$p = Get-Process -Id %d -ErrorAction SilentlyContinue
if ($p) { $p.WaitForExit() }
Move-Item -Force '%s' '%s'
Move-Item -Force $src $dst
Start-Process -FilePath $dst
`,
		exePath+".bak", exePath, os.Getpid(), exePath, staged)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		return fmt.Errorf("写入更新脚本失败: %w", err)
	}
	f.ctx.Bus.Log(moduleID, "info", "已请求管理员权限执行更新，应用将自动重启")
	return sysutil.RunElevated(`powershell -NoProfile -ExecutionPolicy Bypass -File "` + script + `"`)
}

// applyUnix swaps via a shell helper, mirroring applyWindows semantics.
func (f *Feature) applyUnix(staged string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		// Already root: swap in place and restart via /proc/self/exe.
		bak := exe + ".bak"
		_ = os.Rename(exe, bak)
		if err := os.Rename(staged, exe); err != nil {
			_ = os.Rename(bak, exe)
			return err
		}
		return execRestart()
	}
	return fmt.Errorf("应用更新需要以管理员/root 身份运行，或赋予二进制 CAP_LINUX cap_setuid")
}

// execRestart replaces the current process image with the new binary.
func execRestart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// State reports the panel snapshot.
func (f *Feature) State() core.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := core.State{
		"current":          f.last.Current,
		"latest":           f.last.Latest,
		"update_available": f.last.UpdateAvailable,
		"checked_at":       f.last.CheckedAt,
		"asset":            f.last.Asset,
		"staged":           f.last.Staged,
		"err":              f.last.Err,
		"url":              f.last.URL,
		"auto_check":       f.ctx.Config.Get(optAutoCheck, true),
	}
	return st
}

// OnHotkey opens the release page in the browser (manual fallback entry).
func (f *Feature) OnHotkey() error {
	url := f.last.URL
	if url == "" {
		url = strings.TrimSuffix(apiBase, "/releases/latest")
		url = strings.Replace(url, "api.github.com/repos", "github.com", 1)
	}
	return sysutil.OpenURL(url)
}

// fileExists reports whether path exists.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// boolOpt coerces a config option value (any) to bool, defaulting to false.
func boolOpt(v any) bool { b, _ := v.(bool); return b }
