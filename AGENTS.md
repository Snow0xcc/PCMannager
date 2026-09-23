# AGENTS.md — PCMannager

跨平台系统托盘工具（以 Windows 为主，同时支持 Linux/macOS）：剪贴板历史、截图、上下文记录、电脑修复、任务栏状态。Go 编写，模块路径 `github.com/snow0xcc/pcmannager`，Go 1.27。

## 构建、测试、检查、格式化
- 跨平台可构建：Windows 下用 `lxn/walk`/`gonutz/w32` 渲染真实窗口，非 Windows 下对应 UI 文件由 `//go:build !windows` 的同名 no-op 版本替换（UI 降级为空转，核心逻辑照常编译运行）。
- 构建/检查命令：`go build ./...`、`GOOS=windows go build -o pcmannager.exe .`、`go vet ./...`、`gofmt -l .`（格式化用 `gofmt -w .`）。首次改动依赖后先跑 `go mod tidy`。
- 测试：`go test ./...`（建议 `go test -race -count=1 ./internal/... ./modules/...`）。现有单测覆盖 `internal/core`（Bus/Registry/热键解析）、`internal/config`（默认值合并、原子保存）、`internal/server`（httptest 冒烟）、`modules/repair`（工具箱目录）、`modules/screenshot`、`modules/clipboard`（History 并发）、`modules/taskbar`（采集与格式化）。新增功能时应随之补测试。
- **前端禁用 emoji 检查**：`bash scripts/check-emoji.sh`（扫描 html/css/js/md/go，正则覆盖 emoji 与符号区段）。提交前应跑；接入 CI 见 TODO #16。
- `PCMANNAGER_CONFIG` 环境变量可覆盖配置文件路径，便于本地调试。
- **四个目标平台 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` 均以 `CGO_ENABLED=0` 构建通过**（旧 `main.go` 引用已删除 `core` API 的构建缺口已解除）；改完依赖跑 `go mod tidy`，提交前跑 `gofmt -l . | grep -v ^reference/` 与 `go vet ./...`。
- `reference/` 是 5 个第三方参考仓库，以 **git submodule** 引入（见 `.gitmodules`：MineContext、TrafficMonitor、clipboard、screenshot、dtools）。克隆后须 `git submodule update --init --recursive`；**不要**从主代码 import，也不修改其内容——升级只提交子模块指针变更。

## 顶层结构
- `main.go`：程序入口，组装并注册模块，构建托盘菜单，阻塞于托盘消息循环。
- `internal/core/`：核心契约与共享服务——`Module` 接口、`Registry`（模块注册/查找）、`Bus`（SSE 事件广播）、`HotkeyManager`（全局热键）、`Option`/`Action`/`State`（模块配置与状态）、`Tray`/`Logger` 抽象。
- `internal/config/`：配置管理——`Manager`/`ModuleView`/`App`，原子写入、缺省合并，含 `Autostart` 等应用级字段。
- `internal/winui/`：自研**无 cgo** 的 Win32 封装（窗口、任务栏嵌入、DPI、托盘 `Shell_NotifyIconW`、注册表绑定），`_windows.go`/`_other.go` 成对，非 Windows 整体降级为 `errUnsupported`。
- `internal/app/`：应用装配层——持有配置/日志/事件总线/热键路由/**托盘**/**面板服务**；`provider.go` 实现 `server.Provider`（依赖方向 `app -> server`，反向会成环），`app_windows.go`/`app_other.go` 提供 `Run()`（Windows 跑 Win32 消息泵，其它平台等待 ctx）。
- `internal/server/`：首选项面板的 HTTP 层——JSON REST（`/api/state`、`/api/modules[/id]`、`/api/app`、`/api/events` SSE）+ `web/index.html` 通过 `embed` 内置，仅监听 `127.0.0.1`；平台无关、无 cgo，前端禁用 emoji。
- `internal/tray/`、`internal/sysutil/`、`internal/logx/`、`internal/paths/`：支撑包——托盘抽象（`_windows`/`_other` 成对，菜单支持勾选/禁用/分隔符）、系统工具（提权/自启/单实例/通知/命令执行）、日志、统一数据目录解析（`AppName` 仍为历史代号 GoBox）。
- `modules/<name>/`：各功能独立成包（clipboard、screenshot、selfcontext、repair、preferences、taskbar），每个实现 `internal/core.Module`（`NewFeature()` 构造，包名保留旧名如 `statusbar`/`pcrepair`，注意与目录名不同）。
- `modules/repair`：**电脑修复 / 一键安装工具箱**，所有条目集中在 `catalog.go` 的 `Entry` 切片（声明式、平台无关，单一数据源）；`feature.go` 的 `Actions()` 全量从 `Catalog()` 生成，`RunAction` 经 `Lookup`+`Entry.ResolveCommand` 分派；walk 面板（`panel_windows.go`）与 Web 面板均从此目录渲染，新增工具只改 `catalog.go`。包管理器偏好 `prefer_source`（`auto`/`winget`/`choco`）决定解析出的安装命令。
- `docs/DEVELOPMENT.md`：快速开发指南（子模块初始化、工具链、本地调试），新人入职先读。
- `.github/workflows/release.yml`：打 tag 时跨平台构建并发布到 GitHub Release。
- `bin/pcmannager.exe`：已编译的 Windows 二进制，忽略即可。

## 关键开发约定
- 新增模块：在 `modules/<name>/` 下建包实现 `core.Module`，用 `internal/core/registry` 的 `Register` 注册；模块 id（如 `screenshot`、`statusbar`）必须稳定，被 `Registry` 与配置/热键绑定按 id 查找，新增 id 须同步配置结构。
- 平台隔离与降级：Windows 专用 UI 用 `//go:build windows`（`_windows.go`），非 Windows 用 `//go:build !windows` 的 `_other.go` 提供同名 no-op 函数（如 `preferences.Show`、`taskbar` 的 `window` 仅采集日志）。新增平台相关功能须成对补全两端实现，且非 Windows 端不得 import `walk`/`w32`。
- 前端显示规范（硬性约束）：客户端界面**禁止任何 emoji 字符**作为图标或装饰，一律用 icon 资源（SVG/图标字体，置于前端 `assets/icons/`，原生托盘/任务栏走 `internal/winui` 的 `TrayIcon` 等句柄）替代。Wails 前端建立后需在构建中加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）防止混入。此约束优先于"美观/快捷"类考量。
- 热键语法为 `mod+mod+key`（如 `ctrl+alt+f1`），由 `core/hotkey.go` 的 `ParseHotkey`/`ValidHotkey` 解析；全局热键目前为 Windows-first，非 Windows 的 `noopHotkeyBackend` 注册时返回 `errHotkeyUnsupported`。
- **热键线程模型（Windows，P0-2）**：`RegisterHotKey` 会把热键绑定到*调用线程*的消息队列，`WM_HOTKEY` 只投递给该线程。因此 `winHotkeyBackend` 的 `doRegister`/`doUnregister` **只能在 `run()` 所在的 `LockOSThread` 泵线程内调用**；外部 `register/unregister` 一律把命令投递到 `cmdCh` 并同步等待结果，再用 `PostThreadMessage(wmWake)` 唤醒泵。**禁止**从 HTTP handler、main goroutine 或临时 goroutine 直接调用 Win32 注册接口。同理，`HotkeyManager.Bind/Unbind/Stop` 必须在**不持有 `h.mu`** 的情况下调用后端（后端会阻塞等待泵线程）。
- **托盘锁协议（Windows，P0-1）**：`internal/tray` 中 `winTray` 遵循"锁内复制 `NOTIFYICONDATAW` 快照 → 解锁 → 再调 Win32"。`notify()` 自身**不获取任何锁**，任何在持锁路径里调用会重新取锁的 helper 都会造成同锁重入死锁。真实 shell 调用统一经 `notifyFn`（测试可注入替换）。
- **事件总线同步协议（P0-3）**：`Bus` 的锁顺序恒为 `Bus.mu → subscriber.mu`，不可反向。发送只能走 `subscriber.send()`、关闭只能走 `subscriber.close()`；历史在 `Bus.mu` 下**同步预填**（`subBuffer` 256 > `maxHist` 200，故不阻塞），不得再改回异步回放 goroutine——那是"向已关闭 channel 发送"的竞态源。
- 模块通过 `Bus` 发布日志/进度/通知事件供面板（SSE）展示；状态栏走独立配置，其余模块走各自的 `FeatureConfig`。
- 需要重启才生效的配置项：用 `core.Option` 的 `Restart: true` 字段声明，`internal/app` 的 `ApplyOption` 会在应用后自动重启该模块。**禁止**再用 Help 字符串前缀（历史 hack `[restart]`）承载这类元数据——改文案会静默丢失语义，且会把标记泄漏到面板上。

## CI / 发布
- 触发：推送 `v*` tag（如 `v1.2.3`）时由 `.github/workflows/release.yml` 运行。
- 用 `softprops/action-gh-release` 发布，矩阵构建 `windows/amd64`、`linux/amd64`、`darwin/amd64` + `darwin/arm64`，产物命名 `pcmannager-<os>-<arch>[.exe]`。
- 改动构建矩阵、Go 版本或产物命名时，必须同步更新本约定与 workflow 文件。

## 维护规则
当项目结构、构建/测试命令、架构边界、开发约定，或本文件中记录的其他事实发生变化时，必须在同一次改动中同步更新本文件。
