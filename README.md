# PCMannager

跨平台系统托盘工具（以 Windows 为主，同时支持 Linux/macOS），用 Go 编写。集成任务栏状态、剪贴板历史、截图、工作上下文记录、电脑修复工具箱等模块，每个模块可独立开关并绑定全局热键。

**设计原则**：单一二进制、`CGO_ENABLED=0` 纯 Go 构建、零 cgo 依赖（Windows GUI 走自研 `internal/winui`，基于 `syscall.LazyDLL` 直接调 Win32）。

> 规划代号（PRD 与代码内部曾用的 "GoBox"）与本仓库名 PCMannager 指代同一产品，本文档统一使用 **PCMannager**。

## 功能与状态

| 模块 | ID | 说明 | 默认热键 | 默认开关 |
| --- | --- | --- | --- | --- |
| 任务栏状态 | `taskbar` | 任务栏 CPU/内存/网络/磁盘/运行时间小组件（Windows 真实嵌入窗口，其他平台仅采集日志） | `Ctrl+Alt+T` | 开 |
| 剪贴板历史 | `clipboard` | 文本 + 图片历史、置顶、容量裁剪、保留期清理、Ctrl+V 回写（Windows 提供查看器窗口） | `` Ctrl+` `` | 开 |
| 截图 | `screenshot` | 全屏抓取 + 框选编辑器，png/jpg 可选，可自动复制到剪贴板 | `F1` | 开 |
| 上下文记录 | `selfcontext` | 记录活动窗口标题/进程名（不含截屏），可导出/清空 | `Ctrl+Alt+M` | **关**（隐私 opt-in） |
| 电脑修复与工具 | `repair` | 声明式工具箱目录（7 大页、约 60 个工具：系统设置/网络排查/清理/运行环境/包管理器…） | — | 开 |
| 首选项 | `preferences` | 统一设置面板；**非功能模块**，是注册表的视图 | — | — |

模块 ID 是稳定契约，被配置、热键绑定与 REST API 按 ID 查找，新增 ID 须同步 `internal/config`。

## 快速开始

```bash
# 克隆（reference/ 是 submodule，必须一并拉取）
git clone --recurse-submodules <repo-url> pcmannager
cd pcmannager
# 已克隆则补：git submodule update --init --recursive

# 构建并运行（当前平台）
bash scripts/build.sh linux amd64 dist/pcmannager && ./dist/pcmannager

# 调试：用环境变量指向另一份配置目录
PCMANNAGER_CONFIG=/tmp/pcm-conf ./dist/pcmannager
```

启动后 Windows 出现托盘图标；首选项面板监听 `127.0.0.1`（端口取自 `app.server_port`，`0` 表示由 OS 分配），启动日志会打印面板 URL。

## 构建、测试与检查

需要 **Go 1.27+**（`go.mod` 声明 `go 1.27.1`）。

```bash
# 发布构建：唯一入口，CI 与本地同源（对 Windows 自动追加 -H windowsgui）
bash scripts/build.sh windows amd64 dist/pcmannager-windows-amd64.exe
bash scripts/build.sh darwin  arm64  dist/pcmannager-darwin-arm64

# 日常检查
go build ./...
go vet ./...
gofmt -l .                      # 格式化：gofmt -w .
bash scripts/check-emoji.sh     # 前端禁用 emoji 检查

# 测试（当前 175 个用例）
go test -race -count=1 ./internal/... ./modules/...
```

> **Windows 发布必须走 `scripts/build.sh`**：Go 默认按 console 子系统构建，漏掉 `-H windowsgui` 会在启动时弹出黑色控制台窗口。代价是 GUI 子系统下 stdout/stderr 不可见，排障请看数据目录下的 `logs/gobox.log` 或面板的事件日志页。

四个目标平台 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` 均以 `CGO_ENABLED=0` 构建通过。更详细的工具链与本地调试见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

## 架构

```
main.go                  注册 5 个模块 → app.New() → StartPanel/StartTray → Run() 阻塞
  │
  ├─ internal/app        装配层：config / logx / Bus / HotkeyManager / tray / panel
  │                      实现 server.Provider（依赖方向 app → server，反向会成环）
  ├─ internal/core       Module 契约 · Registry · Bus(事件广播) · HotkeyManager
  ├─ internal/config     YAML 配置，原子写入 + 缺省合并
  ├─ internal/server     HTTP REST + SSE，仅监听 127.0.0.1
  │    └─ internal/panel/index.html   面板唯一前端资源（embed 内置，两种传输共用）
  ├─ internal/wailsapp   原生窗口（Wails/WebView2，Windows only；其它平台返回 ErrUnsupported）
  └─ modules/*           taskbar · clipboard · screenshot · selfcontext · repair · preferences
平台层：internal/winui（自研免 cgo Win32：窗口/任务栏嵌入/DPI/托盘/注册表）
        internal/tray · internal/sysutil（提权/自启/单实例/通知）· internal/paths · internal/logx
```

**面板数据单一来源**：HTTP 面板与 Wails 原生窗口渲染同一个 `internal/panel/index.html`（`//go:embed` 不能跨目录，故资源集中于此），且都只读 `server.Provider`，两套 UI 不会漂移。

### 平台隔离策略

Windows 专用 UI 放 `_windows.go`（`//go:build windows`），非 Windows 由 `_other.go`（`//go:build !windows`）提供同名 no-op 函数降级；非 Windows 端不得 import `walk`/`w32`。`internal/winui` 整体在非 Windows 降级为 `errUnsupported`。新增平台相关功能须成对补全两端实现。

### 开发约定（摘要，完整版见 `AGENTS.md`）

- 新增模块实现 `core.Module`（可嵌入 `core.Base`），构造函数统一 `func NewFeature() core.Module`，在 `main.go` 注册。
- 需要重启才生效的配置项用 `core.Option.Restart: true` 声明，**禁止**再用 Help 字符串前缀 `[restart]` 承载这类元数据。
- 日志统一 slog 风格（`ctx.Logger.Info("msg", "k", v)`），禁用 `Infof/Warnf/Errorf`。
- 热键语法 `mod+mod+key`（如 `ctrl+alt+f1`），由 `core.ParseHotkey` 解析；全局热键为 Windows-first，非 Windows 注册返回 `errHotkeyUnsupported`。
- 模块通过 `Bus` 发布日志/进度/通知事件供面板展示；`taskbar` 走独立配置，其余模块走各自 `FeatureConfig`。

## 首选项面板

- **跨平面板（全平台可用）**：`internal/server` 提供 JSON REST + SSE，前端为 `internal/panel/index.html`，仅监听回环地址。
- **原生窗口（Windows，进行中）**：`internal/wailsapp` 用 Wails/WebView2 把同一份前端装进原生窗口。经实测 `wails/v2 v2.10.2` 在 `CGO_ENABLED=0` 下四平台均可编译，不破坏免 cgo 约束。该包已实现 `API` 绑定层（`State`/`Modules`/`Module`/`PatchModule`…，是 `server.Provider` 的薄适配），**尚未在 `internal/app` 接线**；非 Windows 自动回退浏览器面板。

REST 接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/state` | 版本 + 应用设置 + 模块 + 平台能力 |
| GET / PATCH | `/api/app` | 应用级设置（开机自启、主题、日志级别、语言…） |
| GET | `/api/modules`、`/api/modules/{id}` | 模块快照（含当前生效的 option 值） |
| PATCH | `/api/modules/{id}` | 改开关 / 热键 / 选项 |
| POST | `/api/modules/{id}/actions/{action}` | 执行模块动作 |
| POST | `/api/modules/{id}/hotkey`、`/ui` | 触发热键、打开模块窗口 |
| POST | `/api/hotkey/validate` | 热键语法校验 |
| GET | `/api/events` | SSE 事件流（日志 / 进度 / 通知） |

### 前端显示规范：禁用 emoji

客户端界面（Web 面板、Wails 前端与任何原生 UI）**严禁 emoji 字符**作为图标或装饰，一律用 icon 资源替代——SVG / 图标字体置于前端 `assets/icons/`，原生托盘与任务栏走 `internal/winui` 的 `TrayIcon` 等句柄。此约束优先于"美观/快捷"类考量。

已落地自动化检查：`bash scripts/check-emoji.sh`（扫描 html/css/js/md/go，正则覆盖 emoji 与符号区段），并在 `.github/workflows/release.yml` 中作为独立 `lint` job，`build` 通过 `needs: lint` 串在其后——emoji 违规会直接卡住发布。

## 托盘与开机自启

**托盘**：Windows 用 `Shell_NotifyIconW`（`internal/tray` + `internal/winui`），菜单动态生成（打开面板 / 模块开关勾选 / 打开各模块窗口 / 开机自启 / 退出），关闭窗口只回托盘不退出进程。非 Windows 下托盘降级为 no-op，首选项面板取而代之。

**开机自启**：`sysutil.SetAutostart/IsAutostart` 已实现，并在三条路径接线——启动时 `App.SyncAutostart()` 对齐配置与系统状态、托盘菜单开关、面板 `PATCH /api/app`。Windows 写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，Linux 写 `~/.config/autostart/*.desktop`；**macOS 的 launchd 方案尚未实现**。

## 自动更新（规划）

代码中**完全没有**。规划用 GitHub Releases API 比对版本 + 自更新库完成差分下载与替换，托盘菜单加"检查更新"入口，与开机自启等设置同处首选项面板。

## 配置

配置文件为数据目录下的 `config.yaml`（首次运行按 `internal/config.Default()` 生成），数据目录解析见 `internal/paths`：Windows `%APPDATA%\GoBox`、Linux `$XDG_CONFIG_HOME/GoBox`，可由 `app.data_dir` 覆盖。调试时可用 `PCMANNAGER_CONFIG` 指向其它目录（也接受配置文件路径，取其父目录）。

```yaml
app:
  autostart: false
  theme: auto
  log_level: info
  data_dir: ""
  server_port: 0            # 0 = 由 OS 分配
  open_in_webview: false
  language: zh-CN
modules:
  taskbar:     { enabled: true,  hotkey: "Ctrl+Alt+T", options: { interval: 1000, layout: two-line, ... } }
  clipboard:   { enabled: true,  hotkey: "Ctrl+`",     options: { max_items: 500, store_images: true, ... } }
  screenshot:  { enabled: true,  hotkey: "F1",         options: { format: png, jpg_quality: 90, ... } }
  selfcontext: { enabled: false, hotkey: "Ctrl+Alt+M", options: { interval: 300, retention_days: 7, ... } }
  repair:      { enabled: true,  hotkey: "",           options: { prefer_source: auto, confirm_danger: true } }
```

写入是原子的（临时文件 + rename），缺省值在加载时合并，旧配置缺键会自动回填。可在面板中修改，也可手动编辑后重启。

## 发布

推送形如 `v1.2.3` 的 tag 会触发 `.github/workflows/release.yml`：先跑 `lint`（emoji 检查），再矩阵构建 `windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64`，产物命名 `pcmannager-<os>-<arch>[.exe]`，上传并发布到 GitHub Release。

```bash
git tag v1.0.0
git push origin v1.0.0
```

> CI 全流程尚未用真实 tag 实跑验证（见 `TODO.md` #1）。

## 已知限制

- **真实 Windows 验收未完成**：托盘启退循环、热键触发、任务栏小组件嵌入、Explorer 重启恢复等，目前只在 Linux 下通过编译与自动化测试验证。
- **热键可能被占用**：`F1`、`` Ctrl+` `` 在部分机器已被其它程序占用，当前按设计降级（告警 + 面板/托盘仍可用）；面板尚未提供"可用性检测"与改绑引导。
- **命名残留**：`paths.AppName = "GoBox"`、窗口类名 `GoBoxTray`、日志 `gobox.log`、数据目录 `%APPDATA%\GoBox` 与产品名 PCMannager 并存；`modules/taskbar` 包名 `statusbar`、`modules/repair` 包名 `pcrepair` 与目录名不一致。
- **托盘图标无可配置资源**：`defaultIcon()` 目前回退 shell 通用图标。
- **测试覆盖缺口**：`internal/app`、`internal/tray`、`internal/winui`、`modules/preferences` 尚无测试文件。

完整待办与优先级见 [`TODO.md`](TODO.md)，变更历史见 [`CHANGELOG.md`](CHANGELOG.md)，模块开发前请读 [`docs/MODULE-CONTRACT.md`](docs/MODULE-CONTRACT.md)。

## 仓库结构

```
main.go / main_{windows,other}.go   程序入口
internal/{app,core,config,server,panel,wailsapp,winui,tray,sysutil,paths,logx}/
modules/{taskbar,clipboard,screenshot,selfcontext,repair,preferences}/
scripts/{build.sh,check-emoji.sh}   构建与 emoji 检查脚本
docs/                               DEVELOPMENT（开发指南）· MODULE-CONTRACT（模块契约）
                                    · AI-PHASE-A-PROMPT · HANDOVER · PROJECT-AUDIT · competitors
reference/                          5 个第三方参考仓库（git submodule，不参与主构建，勿从中 import）
AGENTS.md / TODO.md / CHANGELOG.md  开发约定 · 待办清单 · 变更历史
```

## 许可证

仓库暂未包含 LICENSE 文件；如需开源分发请先补充。
