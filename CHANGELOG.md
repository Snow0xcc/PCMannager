# 变更日志

本项目尚处于早期搭建阶段，未发布正式版本。以下按提交顺序记录自初始提交以来的演进，便于交接。

所有条目使用 Conventional Commits 风格（`feat`/`fix`/`docs`/`chore`/`build`）。

## 未发布（当前 `main`）

### build — 工程化与发布流水线

- **添加 CI 发布流水线** `6817882`
  - 新增 `.github/workflows/release.yml`：推送 `v*` tag 触发，矩阵构建 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64`，产物命名 `pcmannager-<os>-<arch>[.exe]`，经 `softprops/action-gh-release` 发布到 GitHub Release。

- **将 reference/ 第三方仓库改为 git submodule** `e335318`
  - `MineContext`、`TrafficMonitor`、`clipboard`、`screenshot`、`dtools` 五个第三方仓库由本地嵌套 `.git` 目录改为 submodule 引用各自公共远程，新增 `.gitmodules`。
  - 移除 `.gitignore` 中对 `reference/` 的排除。克隆后需 `git submodule update --init --recursive`。

- **新增 .gitignore** `6817882`
  - 排除编译产物 `bin/`、`*.exe` 与本地日志/临时文件。

### feat — 核心框架与功能模块

- **核心框架 internal/ 与程序入口 main.go** `8f79268`
  - `internal/core`：`Module`/`Registry`/`Bus`(SSE 事件广播)/`HotkeyManager`/`Option`/`Action`/`State` 核心契约与共享服务。
  - `internal/config`：配置管理（`Manager`/`ModuleView`/`App`），YAML 原子写入、模块回填与声明式默认值。
  - `internal/winui`：自研**无 cgo** 的 Win32 封装（窗口、任务栏嵌入、DPI、托盘 `Shell_NotifyIconW`、注册表绑定）。
  - `internal/app`、`internal/tray`、`internal/sysutil`、`internal/logx`、`internal/paths` 等支撑包。
  - 依赖升级至 Go 1.27，模块路径 `github.com/snow0xcc/pcmannager`。

- **功能模块 modules/** `69e917b`
  - `clipboard`、`screenshot`、`selfcontext`、`repair`、`preferences`、`taskbar` 六个模块，Windows 专用 UI 走 `_windows.go`，非 Windows 由 `_other.go` 提供 no-op 降级。

### docs — 文档

- **新增 AGENTS.md 项目指令** `6817882`
  - 面向 AI 编程代理的构建命令、架构边界、平台隔离、前端 emoji 禁用等开发约定。
- **完善 README** `36b384a`
  - 架构/模块/构建说明；规划中的 Wails 首选项面板、托盘保活、开机自启、自动更新；前端禁用 emoji 一律用 icon 替代的硬性规范。
- **新增快速开发指南** `5bea5da`
  - `docs/DEVELOPMENT.md`：Go 1.27 工具链、子模块初始化、常用命令、开发工作流、`PCMANNAGER_CONFIG` 本地调试、已知构建缺口。
- **AGENTS.md 同步结构与支撑包** `aa265f7`
  - 反映 submodule 结构与 `internal/winui`、`app`、`tray`、`sysutil`、`logx`、`paths` 支撑包，并指向开发指南。
- **补充 docs/** `36b384a`
  - `docs/competitors.md` 竞品参考资料。

## 已知问题

- **全平台构建失败（阻塞）**：`windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` 四个目标均报 `undefined: core.App`（`modules/*` 与 `main.go` 仍引用旧 `core` API，而 `internal/core` 已重构为 `Module`/`Registry` 体系）。已实测确认，非交叉编译问题。
- **CI 尚未验证通过**：受上述问题影响，`release.yml` 目前会红；另存在 `go-version` 取值、子模块未初始化、`upload-artifact` 变量作用域等缺陷。
- 详见 [TODO.md](TODO.md) 的 P0/P1 清单。
