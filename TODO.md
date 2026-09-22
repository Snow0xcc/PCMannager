# TODO — 交接与推进清单

面向接手开发者的推进清单，按**优先级 P0→P3**排列。P0 是当前阻塞项，未解决前任何新功能都无法验证发布。

## P0 — 阻塞：构建与 CI 全平台失败

**现状（已实测）**：`windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` 四个目标**全部构建失败**，报错一致：

```
undefined: core.App      (modules/taskbar, clipboard, repair, screenshot, selfcontext)
```

根因：`internal/core` 已重构为 `Module`/`Registry`/`Bus`/`Context` 体系并移除了旧的 `App`/`Manager`/`Feature`，但 `modules/*` 与 `main.go` 仍在引用旧 API。这是**与平台无关的代码错误**，不是交叉编译问题。

推进步骤：

- [ ] **1. 统一 API 映射**：确定采用哪套 —— 让 `main.go` 改用 `internal/app.New()`（推荐，`internal/app/app.go` 已是新装配层），或把旧 `App`/`Manager` 适配层补回 `internal/core`。
- [ ] **2. 迁移 `modules/*`**：`taskbar`、`clipboard`、`repair`、`screenshot`、`selfcontext` 五个模块改为实现 `core.Module`（`Options()/Actions()/State()` + `core.Context` 入参），而非旧 `Init(app *core.App)`。
- [ ] **3. 修复 `hotkey_windows.go`**：`kernel32` 未定义，需补 `syscall.NewLazyDLL("kernel32.dll")` 及其 `NewProc`。
- [ ] **4. 本地复验**：四个目标逐一构建通过（命令见下）。

```bash
for t in "windows amd64" "linux amd64" "darwin amd64" "darwin arm64"; do
  set -- $t; out="pcmannager-$1-$2"; [ "$1" = windows ] && out="$out.exe";
  GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out" . || echo "FAIL $1/$2"
done
```

## P1 — CI 流水线缺陷（构建修好后仍会红）

- [ ] **5. `go-version: '1.27'` 不存在**：`setup-go` 需要具体版本，改为 `1.27.1`（与 `go.mod` 一致）或 `stable`。
- [ ] **6. 缺失 `submodule` 初始化**：`actions/checkout@v4` 默认不拉子模块。需加 `with: submodules: recursive`（`reference/` 为 submodule，虽不参与主构建，但 `go mod tidy` 校验步骤可能受影响）。
- [ ] **7. `upload-artifact` 跨 step 变量失效**：`env.artifact` 在 build 步骤末尾写入 `$GITHUB_ENV`，但 `with:` 在步骤启动前求值，`name` 会拿到空值。改为用 `matrix` 直接计算（`pcmannager-${{ matrix.goos }}-${{ matrix.goarch }}`）。
- [ ] **8. `go mod tidy` 校验过严**：`git diff --exit-code go.mod go.sum` 在 CI 上易因格式化差异误报，且当前源码本身未 tidy 干净。建议先只跑 `go mod tidy`，待 P0 修完再加校验。
- [ ] **9. Windows 产物换行**：Linux runner 生成的 `.exe` 无 CRLF 问题，但若日后加资源文件需配 `.gitattributes`。
- [ ] **10. 验证发布流程**：打一个测试 tag（如 `v0.0.1-rc1`）跑通全流程，确认 4 个产物命名 `pcmannager-<os>-<arch>[.exe]` 并正确上传 Release。

## P2 — 功能补齐（规划中，见 README）

- [ ] **11. 托盘接线**：`internal/tray` 已实现（`Shell_NotifyIcon` + 消息窗口自愈），但**无调用方** —— `internal/app.App` 未持有 tray 字段。需在装配层 `tray.New(log, handler)` 并生成菜单（模块开关/热键/打开面板/退出）。
- [ ] **12. 开机自启**：`config.App.Autostart` 可持久化，但**无人写注册表**。需在 Windows 端用 `internal/winui/registry_windows.go` 已绑定的 API 实现 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的写入/删除；macOS/Linux 另需 launchd/systemd/desktop 方案。
- [ ] **13. 自动更新**：代码中**完全没有**。规划用 GitHub Releases API 比对版本 + `go-github-selfupdate` 类库，托盘菜单加"检查更新"入口。
- [ ] **14. 首选项面板迁移到 Wails**：当前仍是 `lxn/walk` 原生对话框。规划改为 Wails Web 前端（`frontend/`），复用 `internal/config.Manager/ModuleView` 与 `core.Bus` 的 SSE 通道。
  - [ ] 建 `frontend/` 后**必须**加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`），客户端界面禁用 emoji，一律用 icon。
- [ ] **15. 托盘图标资源**：目前 `defaultIcon()` 硬编码回退 shell 通用图标，无自定义图标文件、无配置项。需补图标资源与（可选）配置化路径。

## P3 — 卫生与规范

- [ ] **16. `gofmt` 未达标**：`gofmt -l .`（排除 `reference/`）仍列出 `internal/core/module.go`、`main.go`、`modules/*` 等多文件，需 `gofmt -w .`（注意：会与 P0 的重构改动冲突，建议先重构后统一格式化）。
- [ ] **17. 补测试**：当前**无任何测试文件**。`modules/*` 与 `internal/core`（`Bus`、`Registry`、`HotkeyManager`）优先补单测。
- [ ] **18. 命名不一致**：代码内部代号 `GoBox`（`paths.AppName`、tray 窗口类名 `GoBoxTray`、日志 `gobox.log`）与仓库名 `PCMannager` 并存，需统一（文档已按 PCMannager，代码待改）。
- [ ] **19. 模块目录名与包名不同**：`modules/taskbar` 包名是 `statusbar`、`modules/repair` 包名是 `pcrepair`，易混淆，建议统一。

## 交接须知

- 克隆后必须 `git submodule update --init --recursive`（`reference/` 是 submodule，含 5 个第三方仓库）。
- 本地调试用 `PCMANNAGER_CONFIG=/path/to/config.yaml go run .` 覆盖配置路径。
- 完整构建/调试命令见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)；架构与开发约定见 [AGENTS.md](../AGENTS.md)。
- **在 P0 完成前，不要基于当前 `main` 做功能开发** —— 无法编译验证。
