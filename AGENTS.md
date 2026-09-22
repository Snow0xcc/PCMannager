# AGENTS.md — PCMannager

跨平台系统托盘工具（以 Windows 为主，同时支持 Linux/macOS）：剪贴板历史、截图、上下文记录、电脑修复、任务栏状态。Go 编写，模块路径 `github.com/snow0xcc/pcmannager`，Go 1.27。

## 构建、测试、检查、格式化
- 跨平台可构建：Windows 下用 `lxn/walk`/`gonutz/w32` 渲染真实窗口，非 Windows 下对应 UI 文件由 `//go:build !windows` 的同名 no-op 版本替换（UI 降级为空转，核心逻辑照常编译运行）。
- 构建/检查命令：`go build ./...`、`GOOS=windows go build -o pcmannager.exe .`、`go vet ./...`、`gofmt -l .`（格式化用 `gofmt -w .`）。首次改动依赖后先跑 `go mod tidy`。
- 测试：目前无测试文件，新增功能时应随之补测试。
- `PCMANNAGER_CONFIG` 环境变量可覆盖配置文件路径，便于本地调试。
- `reference/` 是 5 个第三方参考仓库，以 **git submodule** 引入（见 `.gitmodules`：MineContext、TrafficMonitor、clipboard、screenshot、dtools）。克隆后须 `git submodule update --init --recursive`；**不要**从主代码 import，也不修改其内容——升级只提交子模块指针变更。
- **当前已知构建缺口**：`main.go` 仍引用旧 `core` API（`NewManager`/`Manager`/`Feature`/`App`/`NewLogger`），而 `internal/core` 已重构为 `Module`/`Registry`/`Bus` 体系，二者未对齐，CI 构建会失败——修复映射关系属于尚未完成的迁移工作，不属于普通功能开发。

## 顶层结构
- `main.go`：程序入口，组装并注册模块，构建托盘菜单，阻塞于托盘消息循环。
- `internal/core/`：核心契约与共享服务——`Module` 接口、`Registry`（模块注册/查找）、`Bus`（SSE 事件广播）、`HotkeyManager`（全局热键）、`Option`/`Action`/`State`（模块配置与状态）、`Tray`/`Logger` 抽象。
- `internal/config/`：配置管理——`Manager`/`ModuleView`/`App`，原子写入、缺省合并，含 `Autostart` 等应用级字段。
- `internal/winui/`：自研**无 cgo** 的 Win32 封装（窗口、任务栏嵌入、DPI、托盘 `Shell_NotifyIconW`、注册表绑定），`_windows.go`/`_other.go` 成对，非 Windows 整体降级为 `errUnsupported`。
- `internal/app/`、`internal/tray/`、`internal/sysutil/`、`internal/logx/`、`internal/paths/`：支撑包——应用组装（注册表/热键路由/托盘/事件总线）、托盘抽象（`_windows`/`_other` 成对）、系统工具与通知、日志、统一数据目录解析（`AppName` 仍为历史代号 GoBox）。
- `modules/<name>/`：各功能独立成包（clipboard、screenshot、selfcontext、repair、preferences、taskbar），每个实现 `internal/core.Module`（`NewFeature()` 构造，包名保留旧名如 `statusbar`/`pcrepair`，注意与目录名不同）。
- `docs/DEVELOPMENT.md`：快速开发指南（子模块初始化、工具链、本地调试），新人入职先读。
- `.github/workflows/release.yml`：打 tag 时跨平台构建并发布到 GitHub Release。
- `bin/pcmannager.exe`：已编译的 Windows 二进制，忽略即可。

## 关键开发约定
- 新增模块：在 `modules/<name>/` 下建包实现 `core.Module`，用 `internal/core/registry` 的 `Register` 注册；模块 id（如 `screenshot`、`statusbar`）必须稳定，被 `Registry` 与配置/热键绑定按 id 查找，新增 id 须同步配置结构。
- 平台隔离与降级：Windows 专用 UI 用 `//go:build windows`（`_windows.go`），非 Windows 用 `//go:build !windows` 的 `_other.go` 提供同名 no-op 函数（如 `preferences.Show`、`taskbar` 的 `window` 仅采集日志）。新增平台相关功能须成对补全两端实现，且非 Windows 端不得 import `walk`/`w32`。
- 前端显示规范（硬性约束）：客户端界面**禁止任何 emoji 字符**作为图标或装饰，一律用 icon 资源（SVG/图标字体，置于前端 `assets/icons/`，原生托盘/任务栏走 `internal/winui` 的 `TrayIcon` 等句柄）替代。Wails 前端建立后需在构建中加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）防止混入。此约束优先于"美观/快捷"类考量。
- 热键语法为 `mod+mod+key`（如 `ctrl+alt+f1`），由 `core/hotkey.go` 的 `ParseHotkey`/`ValidHotkey` 解析；全局热键目前为 Windows-first，非 Windows 的 `noopHotkeyBackend` 注册时返回 `errHotkeyUnsupported`。
- 模块通过 `Bus` 发布日志/进度/通知事件供面板（SSE）展示；状态栏走独立配置，其余模块走各自的 `FeatureConfig`。

## CI / 发布
- 触发：推送 `v*` tag（如 `v1.2.3`）时由 `.github/workflows/release.yml` 运行。
- 用 `softprops/action-gh-release` 发布，矩阵构建 `windows/amd64`、`linux/amd64`、`darwin/amd64` + `darwin/arm64`，产物命名 `pcmannager-<os>-<arch>[.exe]`。
- 改动构建矩阵、Go 版本或产物命名时，必须同步更新本约定与 workflow 文件。

## 维护规则
当项目结构、构建/测试命令、架构边界、开发约定，或本文件中记录的其他事实发生变化时，必须在同一次改动中同步更新本文件。
