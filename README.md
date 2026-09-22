# PCMannager

跨平台系统托盘工具（以 Windows 为主，同时支持 Linux/macOS），用 Go 编写。集成剪贴板历史、截图、工作上下文记录、电脑修复与工具、任务栏状态等模块，每个模块可独立开关并绑定全局热键。

> 规划代号（PRD/代码内部曾用 "GoBox"）与本仓库名 PCMannager 指代同一产品，本文档统一使用 **PCMannager**。

## 功能模块

| 模块 | 说明 | 默认热键 |
| --- | --- | --- |
| 任务栏状态 (statusbar) | 任务栏 CPU/内存/网络/磁盘监控小组件（Windows 真实窗口，其他平台仅采集日志） | — |
| 剪贴板历史 (clipboard) | 剪贴板历史记录与管理（Windows 提供查看器窗口） | `ctrl+alt+v` |
| 截图 (screenshot) | 截图并编辑（Windows 提供编辑窗口） | `f1` |
| 上下文记录 (selfcontext) | 记录当前工作上下文（活动窗口标题等，Windows 可用） | `ctrl+alt+m` |
| 电脑修复与工具 (pcrepair) | 一键修复动作与工具安装面板（Windows） | `ctrl+alt+r` |
| 首选项 (preferences) | 统一设置面板 | — |

## 架构

- `main.go`：程序入口，组装并注册模块，构建托盘菜单，阻塞于托盘消息循环。
- `internal/core/`：核心契约与共享服务——`Module` 接口、`Registry`(模块注册/查找)、`Bus`(事件广播)、`HotkeyManager`(全局热键)、`Option`/`Action`/`State`(模块配置与状态)。
- `internal/config/`：配置管理（YAML/JSON，含原子写入与缺省合并）。
- `internal/winui/`：自研、无 cgo 的 Win32 封装（窗口创建、任务栏嵌入、DPI、托盘图标、注册表绑定），非 Windows 下整体编译掉（no-op）。
- `modules/<name>/`：各功能模块独立成包，实现 `internal/core.Module`；Windows 专用 UI 放 `_windows.go`（`//go:build windows`），非 Windows 由 `_other.go`（`//go:build !windows`）同名 no-op 函数降级。
- `reference/`：第三方参考实现（MineContext、TrafficMonitor、clipboard、screenshot、dtools 等），仅调研素材，不参与主模块构建。

## 首选项面板（规划：迁移到 Wails Web 前端）

当前首选项面板基于 `lxn/walk` 的 Windows 原生对话框（`modules/preferences/panel_windows.go`）。**规划**将其改写为 **Wails** 驱动的 Web 前端，以获得跨平台一致 UI 与更丰富的交互：

- 技术形态：Go 侧通过 `wails` 暴露绑定方法（读取/写入 `Config`、ReloadHotkeys、应用 Capabilities 检测结果），前端用 HTML/CSS/JS（或轻量框架）渲染，置于 `frontend/` 并由 Wails 打包进二进制。
- 复用现有能力：配置读写走 `internal/config` 的 `Manager`/`ModuleView`；事件流（日志/进度/通知）复用 `internal/core.Bus` 的 SSE 通道推送到前端，避免重复造轮子。
- 平台降级：非 Windows 下 Web 面板仍可打开（不依赖 `walk`/`w32`），与现有 `//go:build !windows` no-op 策略一致。
- 参考 `reference/dtools`（electron-vue）的前端分层与 `reference/MineContext` 的首次运行引导（首次启动安装/配置流程）来设计前端结构。

### 前端显示规范：禁用 emoji，统一用 icon 替代

客户端界面（含 Wails 前端与任何 Web/原生 UI）**严禁使用 emoji 字符**作为图标或装饰。凡需要图形符号的地方，一律使用 **icon** 资源替代：

- 图标来源：优先使用图标字体或 SVG 图标库（如内嵌 SVG / icon font），统一放在前端资源目录（如 `frontend/src/assets/icons/`）并通过 `<svg>`/`<i class="icon-...">` 引用；托盘与任务栏等原生图标继续走 `internal/winui` 的 `TrayIcon` 等原生句柄。
- 禁止范围：按钮、菜单项、状态指示、空状态、提示文案等所有用户可见处，均不得出现 emoji（如 📋 ✂️ 🔔 ⚙️ 等）；文案本身也避免用 emoji 表达语义。
- 落地要求：Wails 前端构建时应在 CI/本地加一道检查（如 `grep -rnP '[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]' frontend/`），防止 emoji 混入源码与产物。
- 现状：前端目录 `frontend/` 尚未建立，本条为前端开发的首条硬性约束，进入实现时即须遵守。

> 状态：Wails 集成**尚未落地**，代码仍依赖 `lxn/walk`，无 `frontend/` 目录、无 `wails` 依赖。

## 托盘保活

程序以系统托盘常驻方式运行，主消息循环阻塞于托盘 `Run` 直至退出。窗口/HWND 通过 `runtime.KeepAlive` 防止被 GC 回收（`internal/winui` 的 `Window.KeepAlive`），托盘图标由 `Shell_NotifyIconW` 管理，最小化/关闭到托盘而非退出进程——这是“保活”的核心机制。非 Windows 下托盘由 `systray` 提供，能力等价降级。

## 开机自启（规划）

配置项 `Autostart`（见 `internal/config`）已定义，Windows 端通过 `internal/winui/registry_windows.go` 的注册表绑定写入：

```
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run\PCMannager
```

指向当前可执行文件路径即可实现登录自启。**现状**：`Autostart` 字段与注册表读取绑定已就绪，但“根据开关实际写/删 `Run` 项”的调用尚未完成，属于规划中功能；macOS/Linux 的自启（launch agent / systemd --user / .desktop）暂未实现。

## 自动更新（规划）

**当前代码不包含自动更新实现。** 规划方案参考 `reference/TrafficMonitor`（独立的更新日志 `UpdateLog` 与版本信息 `version.info` 机制）与 GitHub Release 发布流：

- 发布：推送 `v*` tag 触发 `.github/workflows/release.yml`，跨平台矩阵构建并发布到 GitHub Release（见下文）。
- 更新检查：客户端定时/手动调用 GitHub Releases API 比对版本号（可参考 `version.info` 的版本语义），有更新时提示并下载对应平台产物。
- 实现建议：引入成熟库（如 `github.com/rhysd/go-github-selfupdate`）完成差分下载与替换；更新逻辑须在托盘菜单暴露“检查更新”入口，并与“开机自启”等设置同处首选项面板。

> 状态：自动更新为规划功能，尚无相关代码与依赖。

## 构建

需要 Go 1.27。

```bash
# 当前平台构建（任意平台均可，非 Windows 下 GUI 降级为空转）
go build -o pcmannager .

# 交叉构建 Windows 可执行文件
GOOS=windows go build -o pcmannager.exe .

# 检查与格式化
go vet ./...
gofmt -l .      # 格式化：gofmt -w .
```

> 注意：当前 `main.go` 仍引用旧版 `core` API（`Manager`/`Feature`/`App`），而 `internal/core` 已重构为 `Module`/`Registry` 体系，二者尚未对齐，直接 `go build` 会失败。修复该迁移缺口后才能本地/CI 构建通过。

## 配置

配置文件默认位于用户配置目录下的 `PCMannager/config.json`（路径解析见 `internal/paths`）。可用环境变量 `PCMANNAGER_CONFIG` 覆盖路径以便调试。修改在“首选项”面板中保存，或手动编辑。

## 发布

推送形如 `v1.2.3` 的 tag 会触发 GitHub Actions（`.github/workflows/release.yml`）：跨平台矩阵构建 `windows/linux/darwin(amd64+arm64)`，产物命名 `pcmannager-<os>-<arch>[.exe]`，上传并自动发布到 GitHub Release。

```bash
git tag v1.0.0
git push origin v1.0.0
```

## 许可证

见仓库 LICENSE。
