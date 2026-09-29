# AGENTS.md — PCMannager

跨平台系统托盘工具（以 Windows 为主，同时支持 Linux/macOS）：剪贴板历史、截图、上下文记录、电脑修复、任务栏状态。Go 编写，模块路径 `github.com/snow0xcc/pcmannager`，Go 1.27。

## 构建、测试、检查、格式化
- 跨平台可构建：Windows 下用 `lxn/walk`/`gonutz/w32` 渲染真实窗口，非 Windows 下对应 UI 文件由 `//go:build !windows` 的同名 no-op 版本替换（UI 降级为空转，核心逻辑照常编译运行）。
- 构建/检查命令：`go build ./...`、`go vet ./...`、`gofmt -l .`（格式化用 `gofmt -w .`）。首次改动依赖后先跑 `go mod tidy`。
- **代理/工具链取不到时的兜底**：`proxy.golang.org` 在本机不可达（连 `GOTOOLCHAIN` 自动下载都会 i/o timeout），可用镜像拉工具链与依赖：`GOTOOLCHAIN=go1.27.1 GOPROXY=https://goproxy.cn,direct go mod download`（工具链本身也是经 GOPROXY 下载的模块；下载一次后本机即可离线构建）。
- **发布构建统一走 `bash scripts/build.sh <goos> <goarch> <输出>`**（CI 与本地同源，避免参数漂移）。该脚本固定 `CGO_ENABLED=0 -trimpath -ldflags="-s -w" -tags production`，并**对 Windows 追加 `-H windowsgui`**。两个参数都不能省：
  - 漏掉 `-H windowsgui` → 启动弹出黑色控制台窗口。
  - 漏掉 `-tags production` → Wails 退回 `app_default_windows.go` 桩实现，`CreateApp` 只弹错误框返回 `nil`，随后事件转发以无 Wails 值的 context 调 `runtime.EventsEmit` 触发 `log.Fatalf`，进程在**创建完 config 后静默退出**（GUI 子系统无控制台、无日志，最难排查的一类故障）。
  - GUI 子系统下 stdout/stderr 不可见，排障看日志文件或面板事件日志页。
- **版本号由构建注入**：`internal/app.Version` 是 `var`（非 const），`scripts/build.sh` 用 `-X` 注入；取值优先 `PCM_VERSION` 环境变量（CI 设为 `github.ref_name`，即 tag 名），否则 `git describe`，未打 stamp 时从内嵌 build info 回退、再兜底 `0.0.0-dev`。**任何构建都不会出现空白版本号**，这也是自动更新（TODO #7）的前置条件。
  - **`Version` 的初值必须是 `devVersion` 常量，回退只能写在 `init()` 里**。曾经写成 `var Version = versionFromBuildInfo()`：包级初始化器在**链接期 `-X` 之后**运行，会把注入的 tag 覆盖回内嵌 build info，实测日志显示 `v0.1.0-rc1.0.<日期>+dirty` 而非构建的 `v0.1.0-rc1-8-gd412d3e-dirty`（自动更新的比较基准因此是错的）。现为 `var Version = devVersion` + `init()` 中“仅当仍等于 `devVersion` 才回退”，两条路径都能工作。
- **面板可显示模块失败原因**：`ModuleInfo.LastError`（`last_error` 字段）。Windows 构建跑 `-H windowsgui` 没有控制台，模块"启动失败"或"热键注册失败"若不上报到面板，用户只能翻日志文件。`App` 用 `lastError`/`hotkeyError` 两个槽位分开记录（避免互相覆盖），并给 `lastError` 配 `errorAt` 时间戳——Bus 事件异步到达，用时间戳防止旧事件覆盖更新的状态。
- 测试：`go test ./...`（`-race` 需 cgo，与项目 `CGO_ENABLED=0` 约束冲突，本机不可用）。现有单测覆盖 `internal/core`（Bus/Registry/热键解析与 `Stop` 幂等）、`internal/config`（默认值合并、原子保存）、`internal/server`（httptest 冒烟）、**`internal/app`**（注册/启停/ApplyOption 重启语义/LastError/Shutdown 幂等，仅 `!windows`）、**`internal/logx`**（多 sink 尽力而为写入、ExtraFiles、目录不可用不致命）、**`internal/paths`**（日志路径布局、可执行文件目录解析）、**`internal/winui`**（`Canvas.Image` 全屏尺寸传输、子区域传输、`FillAlpha` 遮罩、`ContrastText` 自适应对比色、`ClipboardFileDrop` 的 `CF_HDROP` 真实往返）、**`modules/taskbar`**（采集与格式化、电量字段、组件定位与反色取字、**网格字段随字号缩放**）、`modules/repair`（工具箱目录）、**`modules/screenshot`**（选项/历史/编码、GIF 帧编码器、滚动条带匹配与多解消歧、编辑器模式与屏幕坐标换算）、`modules/clipboard`（History 并发）、**`modules/preferences`**（nil Manager 容错、ModuleIDs 顺序与去重）、**`modules/updater`**（SemVer 解析与 rc 排序、isNewer opt-in 语义、主机白名单拒绝、原子下载上限、资产平台匹配）、**`internal/logo`**（品牌蝴蝶：渲染非空、16px 下蓝翅与深色机身并存、非法尺寸返回 nil、镜像折返不出界、品牌色锁定）。新增功能时应随之补测试。
- **`Canvas.Image` 必须让 Windows 持有像素**：`StretchDIBits` 在源缓冲区大且位于 **Go 堆**时会静默失败（实测 512×512 成功、768×768 起返回 0 不绘制），导致截图编辑器把全屏捕获画成纯黑。现改为 `CreateDIBSection` 写入后 `BitBlt`/`StretchBlt` 传输；`ImageSubRect` 同理。
- **`AlphaBlend` 的 `BLENDFUNCTION` 按值传**：x64 调用约定下它是 4 字节结构体，打包进一个寄存器（`blend = op | flags<<8 | alpha<<16 | format<<24`），传指针会让调用静默失败——截图遮罩与任务栏半透明都由此而来。`AlphaBlend` 在 `msimg32.dll`，不在 `gdi32`/`user32`。
- **任务栏小组件必须锁线程 + 提升 Z 序 + 颜色键透明**（三条都是实测踩出来的，详见下文“硬约束”）。`runtime.LockOSThread` 缺失会让 Explorer 卡死；不调 `winui.BringToTop` 会被 Windows 11 的 XAML 合成层遮得完全不可见；透明背景靠 `WS_EX_LAYERED` + `LWA_COLORKEY`，并且字体必须用 `NONANTIALIASED_QUAL`，否则 ClearType 混色边缘不受键控制而残留品红描边。
- **日志落两处**：数据目录 `logs/gobox.log`（`paths.LogFile`）+ **程序所在目录** `logs/gobox.log`（`paths.ExecutableLogFile`，随 exe 走 `EvalSymlinks` 解析）。后者用 `Options.ExtraFiles` 接入，是为了让无控制台的 GUI 构建能就地查看日志。同一文件不会重复打开（`sameLogFile` 判重）。
- **日志文件必须轮转**：常驻托盘程序持续写日志，不设上限就是慢性占满磁盘。每个 sink 走 `logx/rotate.go` 的 `rotatingWriter`（默认 8 MiB 上限、保留 3 个 `.1`/`.2`/`.3` 备份；`Options.MaxLogBytes`/`MaxLogBackups` 可覆盖，0 取默认）。**先轮转再写**，避免单条超大记录被拆到两个文件；重命名前先删目标（Windows 下 `os.Rename` 不覆盖已存在文件）；轮转失败保留原句柄继续写（丢日志比文件偏大严重）。
- **写日志不能用 `io.MultiWriter`**：GUI 子系统下 `os.Stderr` 每次写入都返回 "The handle is invalid"，而 `MultiWriter` 在第一个 sink 出错时就短路，会连带丢掉**文件**里的所有记录——恰好发生在唯一没有控制台可回退的构建上。`logx` 因此用 `bestEffort` 多路写入器（单 sink 失败不影响其它），有回归测试。
- **写测试的三个已知坑**（都按实测行为修正过，别再踩）：
  - `App.Enable` 的幂等守卫是 `enabled && running`，**必须经 `EnableModule`**（会先落盘开关）才能触发；直接调 `Enable` 不经过配置写入，守卫不生效。
  - `HotkeyManager.Conflicts()` 只统计**后端真正接受**的 combo，非 Windows 下后端恒返回 `errHotkeyUnsupported`，因此恒为空；冲突检测已由 `internal/core` 的 fake backend 覆盖，其它包别重复断言。
  - `Manager.ModuleIDs()` = 注册顺序 + **配置里独有的模块**，而默认配置永远带 5 个模块，所以断言"等于注册的 N 个"必然失败——应断言前缀顺序。
- **前端禁用 emoji 检查**：`bash scripts/check-emoji.sh`（扫描 html/css/js/md/go，正则覆盖 emoji 与符号区段）。**已接入 CI**：`ci.yml` 的 `lint` job、`release.yml` 的 `lint` job（经 `needs` 串在 `build` 之前，故 emoji 违规会卡住发布且不会随 4 个平台重复执行）与 `pages.yml` 的发布前一步都会跑它（TODO #16 已完成）。
- `PCMANNAGER_CONFIG` 环境变量可覆盖状态目录，便于本地调试：接受目录或配置文件路径（取其父目录），由 `paths.ConfigDirFromEnv()` 统一解析。它必须**同时**生效于 `config.yaml` 与数据目录（日志 / 模块数据）——解析只写在 `main.go` 一处的时代，出现过“配置去了覆盖目录、数据仍在系统默认目录”的分裂，回归测试 `TestConfigDirOverrideMovesConfigAndDataTogether` 守着这点。优先级：`app.data_dir` > `PCMANNAGER_CONFIG` > 系统默认。
- **四个目标平台 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` 均以 `CGO_ENABLED=0` 构建通过**（旧 `main.go` 引用已删除 `core` API 的构建缺口已解除）；改完依赖跑 `go mod tidy`，提交前跑 `gofmt -l . | grep -v ^reference/` 与 `go vet ./...`。
- `reference/` 是 5 个第三方参考仓库，以 **git submodule** 引入（见 `.gitmodules`：MineContext、TrafficMonitor、clipboard、screenshot、dtools）。克隆后须 `git submodule update --init --recursive`；**不要**从主代码 import，也不修改其内容——升级只提交子模块指针变更。

## 顶层结构
- `main.go`：程序入口，组装并注册模块，构建托盘菜单，阻塞于托盘消息循环。
- `internal/core/`：核心契约与共享服务——`Module` 接口、`Registry`（模块注册/查找）、`Bus`（SSE 事件广播）、`HotkeyManager`（全局热键）、`Option`/`Action`/`State`（模块配置与状态）、`Tray`/`Logger` 抽象。
- `internal/config/`：配置管理——`Manager`/`ModuleView`/`App`，原子写入、缺省合并，含 `Autostart` 等应用级字段。
- `internal/winui/`：自研**无 cgo** 的 Win32 封装（窗口、任务栏嵌入、DPI、托盘 `Shell_NotifyIconW`、注册表绑定），`_windows.go`/`_other.go` 成对，非 Windows 整体降级为 `errUnsupported`。
- `internal/app/`：应用装配层——持有配置/日志/事件总线/热键路由/**托盘**/**面板服务**；`provider.go` 实现 `server.Provider`（依赖方向 `app -> server`，反向会成环），`app_windows.go`/`app_other.go` 提供 `Run()`（Windows 跑 Win32 消息泵，其它平台等待 ctx）。
- `internal/panel/`：首选项面板**唯一的前端资源**（`index.html`，`embed` 内置）。HTTP 面板与原生窗口都渲染同一个文件，避免两份前端漂移（`//go:embed` 不能跨目录，故资源集中在此）。
- `internal/wailsapp/`：原生窗口层（Windows 用 Wails/WebView2），`_windows.go`/`_other.go` 成对，非 Windows 返回 `ErrUnsupported` 由调用方回退到 HTTP 面板。**Wails 与 `internal/server` 并存**：二者共用 `server.Provider` 数据契约（经 `App.PanelProvider()`），面板前端通过 `transport` 抽象自动选择 `fetch+EventSource` 或 `window.go.wailsapp.API`。经实测 `wails/v2@v2.10.2` 在 `CGO_ENABLED=0` 下四平台均可编译，不破坏免 cgo 约束。**接线已完成**：`main.go` 调 `runNativeWindow`（Windows 起独立 `LockOSThread` goroutine 跑 `wailsapp.Run`，托盘消息泵必须留在主线程；非 Windows 为 no-op），`internal/app` 的 `OpenPanel()` 按 `app.open_in_webview` 偏好选择原生窗口或浏览器，任一路径失败自动回退另一条。
- `internal/server/`：首选项面板的 HTTP 层——JSON REST（`/api/state`、`/api/modules[/id]`、`/api/app`、`/api/events` SSE）+ `internal/panel/index.html` 通过 `embed` 内置，仅监听 `127.0.0.1`；平台无关、无 cgo，前端禁用 emoji。
- `internal/tray/`、`internal/sysutil/`、`internal/logx/`、`internal/paths/`：支撑包——托盘抽象（`_windows`/`_other` 成对，菜单支持勾选/禁用/分隔符）、系统工具（提权/自启/单实例/通知/命令执行）、日志、统一数据目录解析（`AppName` 仍为历史代号 GoBox）。
- `internal/logo/`：品牌蝴蝶的几何与渲染——`Render(size)` 按 0..100 归一化坐标程序化绘制（刻意不引入 SVG 解析器，保持 `CGO_ENABLED=0` 依赖精简），托盘图标由 `winui.IconFromRGBA` 从它生成 HICON；`svg.go` 把同一套几何导出为静态 SVG（`MarkSVG`/`LogoSVG`），`internal/logo/gen` 是生成命令。文档侧 `docs/site/assets/{logo,icon}.svg` 由 `bash scripts/gen-logo.sh` 生成（`--check` 只校验不写盘），**不要手改**——改形状或配色请改本包几何后重新生成。
- `modules/<name>/`：各功能独立成包（clipboard、screenshot、selfcontext、repair、preferences、taskbar、updater），每个实现 `internal/core.Module`（`NewFeature()` 构造，包名保留旧名如 `statusbar`/`pcrepair`，注意与目录名不同）。
- `modules/updater`：**自动更新**（TODO #7）。定时检查 GitHub Releases（SemVer 比较含 rc 排序与 `include_prerelease` opt-in），下载走主机白名单（api.github.com/github.com/objects.githubusercontent.com，per-dial Control 钩子校验、重定向同样受控）+ 原子暂存 + sha256；`apply_update` 经 `sysutil.RunElevated` 执行 PowerShell helper（等进程退出→备份→替换→重启），**绝不自动执行**下载物。版本基准来自 `core.AppControl.Version()`（新增接口方法，*App 返回 `internal/app.Version`）。默认模块关闭。
- `modules/repair`：**电脑修复 / 一键安装工具箱**，所有条目集中在 `catalog.go` 的 `Entry` 切片（声明式、平台无关，单一数据源）；`feature.go` 的 `Actions()` 全量从 `Catalog()` 生成，`RunAction` 经 `Lookup`+`Entry.ResolveCommand` 分派；walk 面板（`panel_windows.go`）与 Web 面板均从此目录渲染，新增工具只改 `catalog.go`。包管理器偏好 `prefer_source`（`auto`/`winget`/`choco`）决定解析出的安装命令。
- `docs/DEVELOPMENT.md`：快速开发指南（子模块初始化、工具链、本地调试），新人入职先读。
- `.github/workflows/release.yml`：打 tag 时跨平台构建并发布到 GitHub Release。
- **文档与 CI**：`docs/wiki/*.md` 是 Wiki 的**单一数据源**（含 `{TAG}`/`{REPO}` 占位符），由 `scripts/sync-wiki.sh` 渲染后推送到 `<repo>.wiki.git`——该脚本由 `auto-release.yml` 在打完 tag 后调用——**不要**在 Wiki 网页上直接编辑，下次发版会被覆盖。`docs/site/index.html` 是 GitHub Pages 主页源码（纯静态无构建），由 `.github/workflows/pages.yml` 在 `docs/site/**` 变更时发布。
- `bin/pcmannager.exe`：已编译的 Windows 二进制，忽略即可。

## 关键开发约定
- 新增模块：在 `modules/<name>/` 下建包实现 `core.Module`，用 `internal/core/registry` 的 `Register` 注册；模块 id（如 `screenshot`、`statusbar`）必须稳定，被 `Registry` 与配置/热键绑定按 id 查找，新增 id 须同步配置结构。
- 平台隔离与降级：Windows 专用 UI 用 `//go:build windows`（`_windows.go`），非 Windows 用 `//go:build !windows` 的 `_other.go` 提供同名 no-op 函数（如 `preferences.Show`、`taskbar` 的 `window` 仅采集日志）。新增平台相关功能须成对补全两端实现，且非 Windows 端不得 import `walk`/`w32`。
- 前端显示规范（硬性约束）：客户端界面**禁止任何 emoji 字符**作为图标或装饰，一律用 icon 资源（SVG/图标字体，置于前端 `assets/icons/`，原生托盘/任务栏走 `internal/winui` 的 `TrayIcon` 等句柄）替代。Wails 前端建立后需在构建中加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）防止混入。此约束优先于"美观/快捷"类考量。
- 热键语法为 `mod+mod+key`（如 `ctrl+alt+f1`），由 `core/hotkey.go` 的 `ParseHotkey`/`ValidHotkey` 解析；全局热键目前为 Windows-first，非 Windows 的 `noopHotkeyBackend` 注册时返回 `errHotkeyUnsupported`。
- **热键线程模型（Windows，P0-2）**：`RegisterHotKey` 会把热键绑定到*调用线程*的消息队列，`WM_HOTKEY` 只投递给该线程。因此 `winHotkeyBackend` 的 `doRegister`/`doUnregister` **只能在 `run()` 所在的 `LockOSThread` 泵线程内调用**；外部 `register/unregister` 一律把命令投递到 `cmdCh` 并同步等待结果，再用 `PostThreadMessage(wmWake)` 唤醒泵。**禁止**从 HTTP handler、main goroutine 或临时 goroutine 直接调用 Win32 注册接口。同理，`HotkeyManager.Bind/Unbind/Stop` 必须在**不持有 `h.mu`** 的情况下调用后端（后端会阻塞等待泵线程）。
  - `close()` 的 join **必须带超时**（`join()`）：`doneCh` 只由 `run()` 关闭，"未 Start 就 Stop"时它永不关闭，无界等待会让 `Stop` 直至整个应用退出流程挂死。
- **托盘锁协议（Windows，P0-1）**：`internal/tray` 中 `winTray` 遵循"锁内复制 `NOTIFYICONDATAW` 快照 → 解锁 → 再调 Win32"。`notify()` 自身**不获取任何锁**，任何在持锁路径里调用会重新取锁的 helper 都会造成同锁重入死锁。真实 shell 调用统一经 `notifyFn`（测试可注入替换）。
- **Wails 构建标签（P0）**：`wailsapp` 依赖 Wails 的 `production` 标签。缺该标签时 `wails/v2/internal/app/app_default_windows.go` 会选中桩实现，`CreateApp` 弹错误框返回 `nil`，`wails.Run` 立即返回；此时若还有事件转发 goroutine 用 `context.Background()` 调 `runtime.EventsEmit`，它内部 `log.Fatalf` 直接 `os.Exit`——GUI 子系统下就是"静默退出"。`scripts/build.sh` 已固定 `-tags production`，**不要**在别处裸调 `go build`（`go run .` 同样没有该标签，故只能用于控制台调试）。`wailsapp` 侧也做了加固：`API.ctxFor()` 返回 `nil` 而非 `context.Background()`，转发 goroutine 拿到 `nil` 就直接退出。
- **Win32 句柄归属**：`syscall.LazyDLL` 把 DLL 和过程名绑死，从错误 DLL 解析会在**首次调用**时 panic（不是启动时）。已踩过的坑：`SetBkMode`/`SetTextColor` 属于 `gdi32` 而非 `user32`，错挂到 `user32` 会让任务栏小组件首次绘制时崩掉整个进程；`AlphaBlend` 属于 `msimg32`。新增 Win32 调用前先确认导出所在 DLL。
- **无边框窗口拖动**：`WS_POPUP` 窗口没有标题栏，靠 `winui.BeginDragWindow(hwnd)`（`ReleaseCapture` + 合成 `WM_NCLBUTTONDOWN`/`HTCAPTION`）让 Windows 自己跑拖动循环；剪贴板窗口用命中头部的判断决定是拖动还是点选。
- **剪贴板写回必须显式恢复焦点**：查看器窗口在屏时会持有前台，因此“选中一条就想粘到编辑器”靠的是**打开前记住 `winui.FocusedWindow()`**、关闭后 `SetForegroundWindow` 回去（并等窗口真正消失再发 Ctrl+V），而不是依赖当前前台窗口——后者永远是查看器自己。历史记录/写回本身经 `golang.design/x/clipboard`，其实现在 `CGO_ENABLED=0` 下可用（该库仅在无对应平台实现时才退化）。
- **任务栏小组件（`modules/taskbar`）的四条硬约束**（全部踩过坑，改前务必读）：
  - **必须 `runtime.LockOSThread`**：组件是 Explorer `Shell_TrayWnd` 的**跨进程子窗口**，Windows 只把消息投递给窗口的**所属线程**。goroutine 被调度器换线程后，Explorer 在同步 `SendMessage`（`WM_PAINT`/`WM_ERASEBKGND`…）里**永久阻塞，整个任务栏卡死**。`clipboard`/`screenshot` 的窗口同样如此。
  - **必须 `winui.BringToTop`**：Windows 11 任务栏内有一个铺满全条的 XAML 合成层 `Windows.UI.Composition.DesktopWindowContentBridge`，Z 序在普通子窗口之上。不提升就完全不可见（实测：`IsWindowVisible=true` 但 `WindowFromPoint` 命不中、截屏只见任务栏）。定位用 `TaskbarMetricsNow()` 锚在 `TrayNotifyWnd` **左边界**（TrafficMonitor 式），不是任务栏右边缘——后者会压在时钟上；托盘宽度会随图标增减变化，故由独立定时器（2s）重定位。
  - **透明背景用颜色键，且字体必须 `NONANTIALIASED_QUAL`**：GDI 子窗口不合成，不画背景不会露出父窗口，所以用 `WS_EX_LAYERED` + `LWA_COLORKEY`（`winui.SetColorKey`）。但 ClearType 亚像素边缘是与背景的**混色**而非精确键值，会残留品红描边——故透明模式必须用 `winui.NewFontQuality(..., NONANTIALIASED_QUAL)`（硬边缘：要么键色、要么文字色）。文字取**背景反色**（`winui.ContrastText`，Rec.709 亮度）并加粗；采样点在组件矩形**之外**，否则读回的是自己的键色。
  - **重建窗口必须串行 + 等旧窗口真正销毁**：`DestroyWindow` 只能在窗口**所属线程**调用，从模块 goroutine 直调会**静默失败**留下死窗口；`rebuildWidget` 的 stop+start 若无互斥，并发设置变更会丢失旧窗口引用。现用 `WM_CLOSE` 交回窗口自己销毁 + 有界等待，`rebuildMu` 串行化，并在 `spawnWidget` 前 `reapStaleWidgets()` 兜底清扫。否则同一坐标会堆叠多个窗口，表现为**文字重叠**（实测堆到 3 个）。
  - **网格布局的字段宽度必须按字体实测**（`measureGrid` → `computeGridMetrics`，纯函数、`grid_windows_test.go` 守护）。曾经把数值域/单位域/标签域写成固定像素常量（如数值 46px），只在单一字号下对齐；字号调大后字段不够宽，文字互相挤压——就是用户看到的“排版丑”。行高同理必须由字体推出（`2*rowH`），否则两行重叠。改字号默认值/上限时注意 `internal/config` 里的旧值会覆盖新默认值。
- **事件总线同步协议（P0-3）**：`Bus` 的锁顺序恒为 `Bus.mu → subscriber.mu`，不可反向。发送只能走 `subscriber.send()`、关闭只能走 `subscriber.close()`；历史在 `Bus.mu` 下**同步预填**（`subBuffer` 256 > `maxHist` 200，故不阻塞），不得再改回异步回放 goroutine——那是"向已关闭 channel 发送"的竞态源。
- **截图模块（`modules/screenshot`）的三工作流与三条硬约束**：
  - **同一编辑器服务三种模式**（`edModeCapture`/`edModeRecord`/`edModeScroll`，见 `editor.go`）。窗口/遮罩/框选是共用的，只有工具栏（`editorState.buttons()`）与选区动作不同；**新增模式只加按钮 id + 在 `buttons()` 里挂一项**，不要另写一套 overlay。`colorful` 标注仅裁剪模式可用（`beginDraw` 直接拒绝其余模式），否则图形会被烘进录屏/长截图且无法撤销。
  - **录屏只有 GIF**（`recorder.go` + `record_flow.go`）。纯 Go 无成熟 H.264 编码器，项目又约束零 cgo + 无 ffmpeg，因此 **MP4/音频/摄像头/麦克风不可实现**；不要为了“支持 MP4”引入 ffmpeg 外部依赖而不与用户确认。帧缓冲上限 `recMaxFrames=1200`（默认 10fps 约 2 分钟）。录制期间编辑器必须 `parkEditor()` 隐藏，否则会把自己录进去。
  - **滚动拼接必须在“上一帧位置附近”搜索条带**（`scroll.go` 的 `findStripOffset`，从 expected 向外辐射、近距优先）。纯色背景/重复表格行会让条带**多处匹配**，改成全帧自上而下扫描会选到错误位置（回归测试 `TestStitchNoMotionAddsNothing` 会当场抓住）。自动滚动会 **注入真实滚轮**并把光标移到选区中心（`winui.SetCursorPos`+`ScrollWheel`），属“控制用户电脑”的行为，需用户知晓。
  - **需要重启才生效**的配置：无。但驱动器/进程外操作都在超时保护下（如 `shutdownTimeout`）；`Stop()` 会先 `requestStop()` 滚动采样再等 `wg`，否则采样循环会跑到进程退出。
  - **控制条（`editor_control.go`）是与编辑器分开的窗口**，因为编辑器被隐藏后无法承载停止按钮。它与其编辑器**同线程**（`LockOSThread`），`teardown` 里一并销毁——`DestroyWindow` 跨线程会静默失败留下死窗口。
- 模块通过 `Bus` 发布日志/进度/通知事件供面板（SSE）展示；状态栏走独立配置，其余模块走各自的 `FeatureConfig`。
- 需要重启才生效的配置项：用 `core.Option` 的 `Restart: true` 字段声明，`internal/app` 的 `ApplyOption` 会在应用后自动重启该模块。**禁止**再用 Help 字符串前缀（历史 hack `[restart]`）承载这类元数据——改文案会静默丢失语义，且会把标记泄漏到面板上。

## CI / 发布
四个 workflow，职责互斥（改任一触发条件或矩阵都要同步本节）：
- `ci.yml`：**push / PR 到 `main`** 的快速门槛——`lint`（emoji）、`test`（`go test -race`，ubuntu runner 有 cgo）、`build`（四平台矩阵，统一走 `scripts/build.sh` 只验证可编译性）、`vet`（`go vet` + gofmt + **品牌资源一致性**）。
- `auto-release.yml`：**push 到 `main`** 时把最新 semver tag 的 patch 位 +1 并打 tag（脏 tag 一律走兜底：宁可版本保守，也不让流水线挂掉），随后调 `scripts/sync-wiki.sh` 同步 Wiki。它只负责“递增版本号”，构建与发布复用 `release.yml`，避免两处构建参数漂移。
- `release.yml`：推送 `v*` tag 或手动触发时发布——`lint`（emoji）与 `brand`（品牌资源一致性）并行门禁 → `build`（四平台矩阵）→ `release`（断言恰好 4 个产物后用 `softprops/action-gh-release` 发布，说明里带 ghfast / ghproxy 镜像表）。
- `pages.yml`：`docs/site/**` 变更或手动触发时把 `docs/site/` 发布到 GitHub Pages（发布前同样跑 emoji 检查；Pages 需在仓库设置里把 Source 选为 GitHub Actions）。
- 产物命名 `pcmannager-<os>-<arch>[.exe]`，矩阵为 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64`。
- **品牌资源门禁**：改了 `internal/logo` 的几何必须跑 `bash scripts/gen-logo.sh` 重新生成 `docs/site/assets/*.svg`；否则 `ci.yml`（vet job）与 `release.yml`（brand job）会以 `bash scripts/gen-logo.sh --check` 失败拦下——文档上的蝴蝶与托盘图标不允许分叉。
- 改动构建矩阵、Go 版本、产物命名或 workflow 触发条件时，必须同步更新本约定与 workflow 文件。

## 维护规则
当项目结构、构建/测试命令、架构边界、开发约定，或本文件中记录的其他事实发生变化时，必须在同一次改动中同步更新本文件。
