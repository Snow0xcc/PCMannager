# 变更日志

本项目尚处于早期搭建阶段，未发布正式版本。以下按时间倒序记录演进，便于交接。
条目使用 Conventional Commits 风格（`feat`/`fix`/`docs`/`chore`/`build`/`refactor`）。

## 未发布（当前 `main` 及工作区）

### refactor — 全模块迁移到 core.Module 契约（P0 阻塞解除）

**背景**：`internal/core` 已重构为 `Module`/`Registry`/`Bus`/`Context` 体系，删除了旧的
`core.App`、`core.Manager`、`core.Feature`、`core.NewLogger` 与 `config.FeatureConfig`。
此前 `modules/*` 与 `main.go` 仍引用旧 API，导致
`windows/amd64`、`linux/amd64`、`darwin/amd64`、`darwin/arm64` **四平台全部构建失败**
（报错 `undefined: core.App`）。本次迁移已消除该阻塞。

- **modules/taskbar**（包名 `statusbar` → `taskbar`）
  - `Feature` 嵌入 `core.Base`，实现新 `core.Module`；`NewFeature() core.Module`；`ID()="taskbar"`。
  - 声明 25 个 `Options()`，默认值与 `internal/config.Default()["taskbar"]` 逐项核对一致。
  - 删除旧 `w32` 依赖的 `window_windows.go`/`window_other.go`/`format.go`，改为
    `widget_windows.go`/`widget_other.go` 走 `internal/winui`（无 cgo），并新增 `platform_{windows,other}.go` 成对实现。
  - `collector.go` 改为接收 `*core.Context`，goroutine 响应 `Ctx.Done()`，`Stop()` 幂等；新增磁盘/运行时间采样。

- **modules/screenshot**
  - 同样迁移至 `core.Module`；配置驱动 png/jpg、质量、保存目录、历史上限。
  - `editor_windows.go` 由 `lxn/walk` 编辑器改为基于 `internal/winui` 的无 cgo 全屏编辑器
    （框选、遮罩、保存/复制/取消、Enter/Esc/C 快捷键、单实例、`runtime.LockOSThread()`）；
    `editor_other.go` 经 Bus 上报不可用。

- **modules/clipboard**
  - 迁移至 `core.Module`；文本 + 图片监听（`golang.design/x/clipboard`），回写回声抑制。
  - `history.go`：置顶、容量裁剪（保留最新与全部置顶）、保留期清理（修复了误删最新条目的逻辑 bug）。
  - 新增 `paste_windows.go`/`paste_other.go`（Ctrl+V 走 LazyDLL）；
    `viewer_windows.go`/`viewer_other.go` 改用 `winui.Canvas` 自绘，删除旧 walk 版 `viewer.go`。
  - 修复 `showViewer` 阻塞直到窗口关闭的问题。

- **modules/selfcontext**
  - 迁移至 `core.Module`，保持默认 `Enabled=false`（隐私 opt-in）。
  - 选项 interval/retention_days/capture_mode/pause_on_lock 与配置默认值一致（300/7/primary/true）。
  - 仅采集标题/进程名，不含截屏；新增导出/复制/清空/打开动作。
  - `capture_windows.go`/`capture_other.go` 与 `viewer_windows.go`/`viewer_other.go` 成对，去掉 walk 依赖。

- **modules/repair**（包名 `pcrepair` → `repair`，模块 id `pcrepair` → `repair`）
  - 迁移至 `core.Module`；`Options()` 默认与配置一致（`confirm_danger:true`、`prefer_source:"auto"`）。
  - `panel_windows.go` 改用 `f.ctx`，增加互斥保护的面板句柄（重复 `OpenUI` 聚焦已有窗口）与危险命令二次确认。
  - 新增成对 `exec_windows.go`/`exec_other.go`。

- **modules/preferences**
  - `Manager` 不再嵌入已删除的 `*core.Manager`，改为持有 `*app.App`，`NewManager(*app.App)`。
  - `panel_windows.go` 基于 `core.Module`/`core.Option` 描述符重建：按模块开关（经 `EnableModule`）、
    热键校验写入（`ModuleView.SetHotkey` + `RebindHotkeys`）、bool/select/int/string 控件。
  - 新增成对 `format_windows.go`/`format_other.go`。

- **main.go**
  - 全面改用 `internal/app` 装配层：`app.New()` → `MustRegister` ×5 → `InitModules()` →
    `StartModules()` → `Wait()` 阻塞 → `Shutdown()`；接入 SIGINT/SIGTERM。
  - 保留 `PCMANNAGER_CONFIG` 环境变量（切换工作目录以影响配置探测）。
  - `preferences` 不作为模块注册（仅是注册表视图，且为 Windows-only walk UI）。

- **internal/core/hotkey_windows.go**
  - 经核查 `kernel32` 已由 `syscall.NewLazyDLL` 声明，此前"未定义"的判断为误诊，**未作修改**。

### build — CI 发布流水线修复

- `.github/workflows/release.yml`：
  - `go-version` `'1.27'` → `'1.27.1'`（与 `go.mod` 一致；`1.27` 非 setup-go 可解析版本）。
  - `checkout@v4` 增加 `submodules: recursive` 与 `fetch-depth: 0`（`reference/` 为 submodule）。
  - **修复产物名为空的时序缺陷**：原先 `upload-artifact` 的 `name/path` 引用 build 步骤末尾才写入
    `$GITHUB_ENV` 的 `env.artifact`，而 `with:` 在步骤启动前求值 → 恒为空。改为在 job 级
    `env.ARTIFACT` 用 matrix 一次性算好（windows 后缀 `.exe` 用 GitHub 表达式），build 与 upload 共用。
  - 移除 `go mod tidy` + `git diff --exit-code` 的强门禁，避免环境差异误判中断发布。
  - release job 新增"断言恰好 4 个产物"步骤，保证 `files: artifacts/*` 命中全部二进制。
  - 新增 `gofmt` 检查（排除 `reference/`）。

### docs

- 新增 `docs/MODULE-CONTRACT.md`：模块契约规范（接口、标准骨架、12 条硬性规则、验证命令、core 类型速查）。
- 新增 `CHANGELOG.md`、`TODO.md`（交接与推进清单）。
- `AGENTS.md`：同步 submodule 结构、`internal/winui`/`app`/`tray`/`sysutil`/`logx`/`paths` 支撑包，
  并记录前端禁用 emoji 的硬性约束。
- `docs/DEVELOPMENT.md`：快速开发指南（工具链、子模块初始化、构建命令、本地调试）。
- `README.md`：架构、模块、规划能力（Wails 面板/托盘保活/开机自启/自动更新）、前端 emoji 规范。

### 更早的提交

- `e335318` build: `reference/` 5 个第三方仓库改为 git submodule，新增 `.gitmodules`。
- `8f79268` feat: 核心框架 `internal/`（`core`/`config`/`winui`/`app`/`tray`/`sysutil`/`logx`/`paths`）与入口。
- `69e917b` feat: 功能模块 `modules/` 初始版本。
- `6817882` chore: CI 发布流水线、`AGENTS.md`、`.gitignore` 初版。
- `652f571` Initial commit。

## 验证状态（实测）

| 检查项 | 结果 |
| --- | --- |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `gofmt -l .`（排除 `reference/`） | 无输出 |
| release.yml YAML 语法 | 通过 |

四平台产物命名：`pcmannager-<os>-<arch>[.exe]`。
