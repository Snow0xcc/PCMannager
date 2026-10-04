// Package updater — module wiring: options, actions, periodic check loop,
// download staging and the apply-update flow.
package updater

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/snow0xcc/pcmannager/internal/core"
	"github.com/snow0xcc/pcmannager/internal/paths"
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

	// —— 测试 seam（零值 = 生产行为）——
	// firstCheckDelay 覆盖 loop 的首检延迟（默认 1 分钟）。
	firstCheckDelay time.Duration
	// checkInterval 覆盖 loop 的后续间隔（默认读 interval_hours 配置）。
	checkInterval time.Duration
	// httpClientFn 覆盖出站 HTTP 客户端（默认 newHTTPClient 白名单客户端）。
	httpClientFn func() *http.Client

	// stopCh 关闭即终止轮询 goroutine；Start 创建、Stop 关闭并置 nil。
	stopCh chan struct{}
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
	return "静默检查 GitHub Releases 新版本（结果仅写入面板事件流，不打扰）；可下载暂存更新包，应用更新需在面板显式触发（需管理员确认）"
}

// Options declares the module settings.
func (f *Feature) Options() []core.Option {
	return []core.Option{
		{Key: optAutoCheck, Label: "自动检查更新", Kind: core.KindBool, Default: true, Restart: true,
			Help: "按固定间隔检查 GitHub Releases；关闭后仅可手动检查"},
		{Key: optIntervalH, Label: "检查间隔（小时）", Kind: core.KindInt, Default: 24, Min: 1, Max: 168, Step: 1,
			Help: "两次自动检查之间的间隔，1-168 小时"},
		{Key: optIncludePre, Label: "包含预发布版本", Kind: core.KindBool, Default: false,
			Help: "是否将 rc/预发布 tag 视为可用更新"},
		{Key: optNotify, Label: "在面板事件流提示发现新版本", Kind: core.KindBool, Default: true,
			Help: "检查/下载结果始终写入面板右上角事件流；本开关仅控制是否额外记录一条醒目的事件日志"},
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

// Init stores the context and seeds state. The periodic check loop is NOT
// started here: app only calls Start for enabled modules, so starting in Init
// would make the default-off updater poll GitHub anyway (A4).
//
// consumeUpdateLog is safe to run here and needs no network: it only reads and
// removes the local record left behind by the apply helper of a previous run.
func (f *Feature) Init(ctx *core.Context) error {
	f.ctx = ctx
	f.setCurrent(f.currentVersion())
	// A stale staged file from a previous run is still applicable.
	if p := filepath.Join(ctx.DataDir, stagedName); fileExists(p) {
		f.staged = p
	}
	f.consumeUpdateLog()
	return nil
}

// Start implements core.Module: it publishes the initial state so the panel
// shows the module as running with the detected version, and opens the
// periodic check loop (only reached for enabled modules — the app gates
// Start on the enabled flag).
func (f *Feature) Start() error {
	f.setCurrent(f.currentVersion())
	if f.ctx != nil {
		f.ctx.Bus.State(moduleID, f.State())
	}
	if boolOpt(f.ctx.Config.Get(optAutoCheck, true)) {
		f.mu.Lock()
		if f.stopCh == nil { // 已在轮询则不重复起 goroutine
			f.stopCh = make(chan struct{})
			go f.loop(f.stopCh)
		}
		f.mu.Unlock()
	}
	return nil
}

// Stop implements core.Module: it closes the check loop's stop channel,
// signaling the goroutine to exit (loop also honors ctx cancellation). A
// staged download is intentionally kept: the user may apply it after
// restarting into this or a later session.
func (f *Feature) Stop() error {
	f.mu.Lock()
	ch := f.stopCh
	f.stopCh = nil
	f.mu.Unlock()
	if ch != nil {
		close(ch)
	}
	return nil
}

// loop runs the periodic check until app shutdown or the module is stopped.
// stop is owned by the caller (Start/Stop lifecycle); ctx cancellation still
// exits for shutdown.
func (f *Feature) loop(stop <-chan struct{}) {
	// First check shortly after boot so a fresh release is noticed promptly;
	// later checks follow the configured interval.
	first := f.firstCheckDelay
	if first <= 0 {
		first = time.Minute
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-f.ctx.Ctx.Done():
			return
		case <-t.C:
			if boolOpt(f.ctx.Config.Get(optAutoCheck, true)) {
				if _, err := f.Check(f.ctx.Ctx); err != nil {
					f.ctx.Logger.Warn("自动检查更新失败", "err", err)
				}
			}
			next := f.checkInterval
			if next <= 0 {
				next = f.interval()
			}
			t.Reset(next)
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

// setCurrent records the running version. f.last is read concurrently by
// State() (panel polling), so every write must hold f.mu (review I-1).
func (f *Feature) setCurrent(v string) {
	f.mu.Lock()
	f.last.Current = v
	f.mu.Unlock()
}

// client returns the outbound HTTP client, honoring the test seam.
func (f *Feature) client() *http.Client {
	if f.httpClientFn != nil {
		return f.httpClientFn()
	}
	return newHTTPClient()
}

// currentVersion reports the running version, preferring the trusted baseline
// from core.AppControl.Version() (implemented by *App).
//
// ctx.App.Version() is internal/app.Version — the tag stamped at link time — and
// is the only trustworthy comparison baseline for auto-update. Reading
// debug.ReadBuildInfo() directly used to yield bogus values like
// "v0.1.0-rc1.0.<date>+dirty" instead of the injected tag, and made isNewer
// flag the current tag itself as an update (A3). The AppControl seam exists so
// modules get that baseline without importing internal/app, which would invert
// the modules→app dependency.
//
// The build-info fallback below therefore only fires when App is unavailable
// (unit tests, or a host that leaves App nil) — it is not on the release path.
func (f *Feature) currentVersion() string {
	if f.ctx != nil && f.ctx.App != nil {
		if v := f.ctx.App.Version(); v != "" {
			return v
		}
	}
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

	rel, err := fetchLatest(ctx, f.client())
	res := f.recordCheck(rel, err)
	if f.ctx != nil {
		f.ctx.Bus.State(moduleID, f.State())
	}
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
			// 静默更新：不再弹系统通知。optNotify 的语义已反转为“在面板
			// 事件流记录一条醒目提示”，面板右上角的事件流就是“界面角落的
			// 无感提示”；关闭后仍写状态，只是不发这条事件。
			if boolOpt(f.ctx.Config.Get(optNotify, true)) {
				f.ctx.Bus.Log(moduleID, "info", "发现新版本 "+rel.TagName+"（当前 "+f.last.Current+"）")
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
	hc := f.client()
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
	f.ctx.Bus.State(moduleID, f.State())
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
// exit, backs up the current exe, swaps in the staged update, restarts with
// the original command-line arguments and records the result in update.log.
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
	if err := os.WriteFile(script, []byte(buildUpdateScript(exePath, staged, argsPayload(os.Args[1:]), f.last.Latest)), 0o755); err != nil {
		return fmt.Errorf("写入更新脚本失败: %w", err)
	}
	f.ctx.Bus.Log(moduleID, "info", "已请求管理员权限执行更新，应用将自动重启")
	return sysutil.RunElevated(`powershell -NoProfile -ExecutionPolicy Bypass -File "` + script + `"`)
}

// argsPayload encodes the original process arguments for the update helper.
//
// 主程序是单一进程，面板/窗口都是它的窗口而非独立进程，所以重启这一个 exe
// 就等于“唤醒用户之前在用的程序实体”；但要带着原参数——托盘常驻与测试
// 运行可能依赖它们。os.Args[0] 已由 Start-Process -FilePath 指定，只透传
// 下标 1 起的参数。base64 避开了 PowerShell 引号转义问题（其字符集
// A-Za-z0-9+/= 不含引号，可安全地放入单引号字符串）。
func argsPayload(args []string) string {
	if len(args) == 0 {
		return ""
	}
	// \n 作分隔符是安全的：CreateProcess 的命令行是单行字符串，Windows
	// 进程参数不可能含原始换行，所以分隔符不会与参数内容冲突。
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(args, "\n")))
}

// argsFromPayload decodes an argsPayload back into the argument list.
func argsFromPayload(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(dec) == 0 {
		return nil, nil
	}
	return strings.Split(string(dec), "\n"), nil
}

// buildUpdateScript renders the elevated PowerShell helper. Kept a pure
// function so the quoting/base64 round-trip is unit-testable.
func buildUpdateScript(exePath, staged, argsB64, latestTag string) string {
	tag := latestTag
	if tag == "" {
		tag = "unknown"
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$src = '%s'
$dst = '%s'
$argsB64 = '%s'
$p = Get-Process -Id %d -ErrorAction SilentlyContinue
if ($p) { $p.WaitForExit() }
Move-Item -Force '%s' '%s'
Move-Item -Force $src $dst
# 透传原进程命令行参数，让重启后的进程回到用户更新前正在使用的状态。
$argList = @()
if ($argsB64) {
  $dec = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($argsB64))
  if ($dec) { $argList = $dec -split "\n" }
}
if ($argList.Count -gt 0) { Start-Process -FilePath $dst -ArgumentList $argList }
else { Start-Process -FilePath $dst }
# 写一行完成记录：新进程启动时消费它并在面板报“更新完成”。
$logDir = Join-Path $env:APPDATA 'GoBox\logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$stamp = Get-Date -Format 'yyyy-MM-ddTHH:mm:ss'
Add-Content -Path (Join-Path $logDir 'update.log') -Value ($stamp + [char]9 + '%s' + [char]9 + $dst) -Encoding UTF8
`,
		staged, exePath, argsB64, os.Getpid(), exePath+".bak", exePath, tag)
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

// updateDoneLog resolves the update.log path the elevated helper writes.
//
// helper 固定写默认数据根（%APPDATA%\GoBox\logs\update.log），这里用同一个
// 解析（paths.DataDir("")），保证“写”与“读”落盘位置一致，即使用户自定义
// 了 data_dir 也只是留下一条未消费的旧记录，不会误报。
func updateDoneLog() (string, error) {
	root, err := paths.DataDir("")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "logs", "update.log"), nil
}

// consumeUpdateLog reports a completed update from the previous run and
// consumes the record file.
//
// 记录由 applyWindows 的 helper 在替换+重启后写入；新进程 Init 时若发现
// 带当天时间戳的成功记录，就在面板事件流报“已更新到 vX.Y.Z”，然后删除
// 文件。非当天的记录视为残留（例如用户改过系统时间），直接删除不提示。
// 解析失败同样删除：宁可丢一次提示，也不让旧文件每次启动都重复报喜。
func (f *Feature) consumeUpdateLog() {
	if f.ctx == nil {
		return
	}
	p, err := updateDoneLog()
	if err != nil || !fileExists(p) {
		return
	}
	data, readErr := os.ReadFile(p)
	// 先消费再决定提示，避免提前 return 泄漏旧记录。
	defer func() {
		_ = os.Remove(p) // best-effort; a leftover record only risks a stale notice
	}()
	if readErr != nil {
		return
	}
	tag := parseUpdateLogEntry(string(data))
	if tag == "" {
		return
	}
	if tag == "unknown" {
		f.ctx.Bus.Log(moduleID, "info", "更新已完成，程序已重启到新版本")
	} else {
		f.ctx.Bus.Log(moduleID, "info", "更新已完成：已更新到 "+tag)
	}
}

// parseUpdateLogEntry validates the last success record in update.log.
// Format per line: <RFC3339>\t<tag>\t<exe>. It returns the tag only when the
// record is from today; "" means no valid entry (caller must not report).
func parseUpdateLogEntry(data string) string {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	if len(lines) == 0 {
		return ""
	}
	line := strings.TrimRight(lines[len(lines)-1], "\r")
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) < 3 {
		return ""
	}
	stamp, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return ""
	}
	// RFC3339 自带偏移，Parse 返回固定偏移 Location；“当天”以本机时区的
	// 日历日为准，所以先归一到 Local 再比。
	local := stamp.Local()
	today := time.Now()
	if local.Year() != today.Year() || local.YearDay() != today.YearDay() {
		return ""
	}
	return parts[1]
}

// boolOpt coerces a config option value (any) to bool, defaulting to false.
func boolOpt(v any) bool { b, _ := v.(bool); return b }
