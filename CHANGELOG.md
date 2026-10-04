# 变更日志

本项目尚处于早期搭建阶段，未发布正式版本。以下按时间倒序记录演进，便于交接。
条目使用 Conventional Commits 风格（`feat`/`fix`/`docs`/`chore`/`build`/`refactor`）。

## 未发布（当前 `main` 及工作区）

### feat — 新增 launcher 模块：全局快捷面板与超级面板

`modules/launcher` 已实现并注册（`main.go` 中 `MustRegister(launcher.NewFeature())`，排在 updater 之后），但此前 `README.md` / `AGENTS.md` / `CHANGELOG.md` 均未收录，本次一并补齐文档。模块要点：

- `commands.go` 统一构建候选表：模块动作、模块界面入口、网页捷径、本地应用索引（`apps.go`）。
- `ranks.go` 持久化置顶与打开次数，按 `置顶 ×10^6 + 匹配度 ×10^3 + 预设优先级 ×10 + 打开次数 ×2` 打分排序。
- `pinyin.go` 走 `mozillazg/go-pinyin` 生成拼音首字母缩写，作为别名并入关键字索引；别名与缩写可在右键"编辑关键字"中修改。
- 超级面板（`super.go` + `super_windows.go`）是独立的悬浮磁贴池，`Alt+P` 唤起；固定项以命令 key 持久化到 `launcher_super.json`，key→command 反查用当前活表，模块停用后对应条目静默跳过。
- **置顶（pinned，只影响排序）与超级面板固定（superStore）是两套独立语义**，勿混用。
- 热键默认 `alt+space`（系统菜单键，常被 PowerToys Run 等占用），按设计 best-effort：注册失败经面板热键错误上报，面板本身仍可经托盘打开。
- `resolveKeys` 放在平台无关文件（反查只依赖内存命令表），避免非 Windows nil 桩导致 Linux runner 测试失败。

配置项：`hotkey`（默认 `alt+space`）、`max_results`（默认 8，范围 3–20）。

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

### chore — 仓库状态同步（文档与 gitignore 对齐代码）

本次复核（`2026-10-04`）只改文档与 `.gitignore`，**未触碰任何 Go 代码**，因此四平台构建与测试结果与上一条验证状态一致。发现并修正的失配：

- **`.DS_Store` 未被忽略**：仓库根、`docs/`、`internal/`、`modules/`、`.github/`、`reference/` 共 6 处 Finder 元数据文件一直冒在 `git status` 未跟踪列表里。已在 `.gitignore` 补 `.DS_Store`，`git status` 现只剩真实改动。
- **`AGENTS.md` 仍写 `bin/pcmannager.exe` 是已编译二进制**：实际构建输出早已改到 `dist/`（`.gitignore` 忽略），`bin/` 目录已不存在。AGENTS.md 与 `docs/DEVELOPMENT.md` 的提交说明均改为 `dist/`。
- 上一条已记录的文档补齐（README 模块表 / 测试数 / 自动更新章节、AGENTS.md launcher 条目、CHANGELOG launcher feat）保持不变。

**未随本次改动推送的既有问题**（需人工决策，本次不自动处理）：

- **本地领先 `origin/main` 6 个提交**，均为 2026-09-30 ~ 10-03 的实质工作（品牌 logo 接入、`PCMANNAGER_CONFIG` 双目录修复、macOS/Linux 托盘与角标、macOS sysutil、Linux 托盘 D-Bus 集成测试），**尚未 push**。> 后续经 GitHub API 复核：远端已领先 48 个提交，双方**已分叉**，详见下文「远端状态」。
- **`v0.1.0-rc1` tag 已确认存在于远端，无需处理**：`git ls-remote --tags origin` 在本机因网络受限超时（`Recv failure: Operation timed out`），改用 GitHub API 按 `refs/tags/v0.1.0-rc1` 取 `README.md` 成功（返回 rc1 时期的旧文档），并用一个不存在的 tag 名 `v0.1.0-rc-doesnotexist` 反向验证该接口对无效 ref 会返回 404，证明前一次是真实解析而非静默回退。补充说明：本地 tag 为 annotated tag（对象 `b513d0a`），解引用后指向提交 `500507a`，该提交已包含在 `origin/main` 中。
- **`reference/` 5 个 submodule 全部未初始化**（`git submodule status` 每行以 `-` 开头）。不影响构建（主代码不从 reference import），但按 `docs/DEVELOPMENT.md` 的交接约定应执行 `git submodule update --init --recursive`。
### docs

- 新增 `docs/MODULE-CONTRACT.md`：模块契约规范（接口、标准骨架、12 条硬性规则、验证命令、core 类型速查）。
- 新增 `CHANGELOG.md`、`TODO.md`（交接与推进清单）。
- `AGENTS.md`：同步 submodule 结构、`internal/winui`/`app`/`tray`/`sysutil`/`logx`/`paths` 支撑包，
  并记录前端禁用 emoji 的硬性约束。
- `docs/DEVELOPMENT.md`：快速开发指南（工具链、子模块初始化、构建命令、本地调试）。
- `README.md`：架构、模块、规划能力（Wails 面板/托盘保活/开机自启/自动更新）、前端 emoji 规范。
- fix：`PCMANNAGER_CONFIG` 只搬走了 `config.yaml`，数据目录（日志、模块数据）仍落系统默认目录，导致面板 `effective_data_dir` 与实际写入位置不一致。解析收敛到 `paths.ConfigDirFromEnv()`，`internal/app` 解析数据目录时纳入覆盖目录（优先级 `app.data_dir` > 环境变量 > 系统默认）；顺带删掉 `main.go` 里被复制了两遍的 `configDirEnv` 注释。
- feat(sysutil): macOS 平台实现落地——开机自启走 launchd LaunchAgent
  （`~/Library/LaunchAgents/cc.snow0xcc.gobox.plist`，登录时自动加载，此前 README 明确标注未实现）；
  `ShowInFolder`/`OpenTerminalHere`/`OpenURL` 从 xdg-open 分支中拆出，改走系统 `open`
  （此前在 macOS 上必然报"未找到"）。sysutil 拆为 windows/darwin/other(!w&&!d)/unix 四份，
  自启往返用同一份单测在三平台各自验证（macOS 侧加 `plutil -lint` 权威校验）。
- feat(tray): 托盘角标——`Tray.SetBadge` 三平台实现，绘制统一在 `badge.go` 的 `BadgeOverlay`（红色胶囊 + 3x5 手工点阵数字，纯 Go 无字库依赖）；默认接线为剪贴板历史条数实时驱动（`app.badgeWatch`），模块停用或清空即清除；Windows 侧换 HICON 时只销毁自己铸造的句柄（初始图标可能是 shell 的 LR_SHARED，不可销毁）。
- feat(tray): 补齐 macOS 与 Linux 托盘——macOS 走 AppKit `NSStatusBar`（经 purego 的 Objective-C runtime，仍保持 `CGO_ENABLED=0`；显式 dlopen Foundation/AppKit，UI 操作统一派发回主线程，Run 在主线程泵 CFRunLoop），Linux 走 `StatusNotifierItem` + `com.canonical.dbusmenu`（纯 Go D-Bus，godbus）；新增 `tray.Supported()`，面板 `capabilities.tray_icon` 据实上报，不再按平台写死 false。
- 品牌 logo 接入 README 页眉、Wiki `Home.md` 与 Pages 站点（含 favicon）：`docs/site/assets/logo.svg`（图标 + 字标）与 `icon.svg`（仅图标）由新增的 `scripts/gen-logo.sh` 从 `internal/logo` 的蝴蝶几何导出（`internal/logo/svg.go` + `internal/logo/gen`），与托盘图标同一品牌；`--check` 可用于校验资源是否与几何一致。

### 更早的提交

- `e335318` build: `reference/` 5 个第三方仓库改为 git submodule，新增 `.gitmodules`。
- `8f79268` feat: 核心框架 `internal/`（`core`/`config`/`winui`/`app`/`tray`/`sysutil`/`logx`/`paths`）与入口。
- `69e917b` feat: 功能模块 `modules/` 初始版本。
- `6817882` chore: CI 发布流水线、`AGENTS.md`、`.gitignore` 初版。
- `652f571` Initial commit。

## 远端状态（2026-10-04，经 GitHub API 核对）

网络 `git fetch/ls-remote` 在本机被沙箱拦截，故改用 GitHub API 取得权威远端状态。结论与上文"本地领先 6 个提交"相比**有重要修正**：

| 项目 | 状态 |
| --- | --- |
| 远端 `main` HEAD | `dbb64d6`（2026-10-04T03:31:06Z，作者 RustCode） |
| 本地 `main` HEAD | `a11bb0d`（2026-10-03） |
| merge-base | `97877a7`（"docs(pages): 补充 Pages 启用时的排查指引"） |
| 分叉 | **远端领先 48 个提交，本地领先 6 个提交** |
| `v0.1.0-rc1` tag | 已确认存在于远端（API 取 `refs/tags/v0.1.0-rc1` 成功，不存在的 tag 会 404） |

注意：上文"`git status -sb` 显示 `[ahead 6]`"是**当时**的观测；本地 `origin/main` 跟踪引用其内容已为 `dbb64d6`，但本地工作区仍停在 6 个提交之前。**两者不是简单的"领先 6 个"，而是已经分叉。**

### 远端新增内容（本轮不可见的 48 个提交）

- `docs/ROADMAP.md`（392 行）、`docs/RELEASE-CHECKLIST.md`、`docs/NAMING-MIGRATION.md`、`docs/AGENT-SETUP-HANDOVER.md`。
- `.rustcode/`（skills 与 commands 入库）、`.github/dependabot.yml`。
- 模块增强：截图历史与上下文记录条目列表（`modules/screenshot/panel.go`、`modules/selfcontext/panel.go`）、updater 生命周期、taskbar 采集调整。
- 依赖升级：Wails `2.10.2 → 2.16.0`、`golang.design/x/clipboard 0.9.0 → 0.11.0`、gopsutil v4、`golang.org/x` 安全升级。
- CI 修复：`auto-release.yml` tab 缩进导致的启动失败、`GITHUB_TOKEN` 推 tag 不触发 `release.yml` 的第 2 轮方案。

### 与本文档改动直接冲突的远端变更（需人工裁决）

1. **品牌资源管线已被替换**：远端**删除**了 `scripts/gen-logo.sh` 与 `docs/site/assets/{logo,icon}.svg`，改为 `scripts/genicon.go`（`//go:build ignore`，`go run scripts/genicon.go`）生成并**提交** `internal/tray/assets/icon.ico`（16/32/48/64/256 px，PNG-in-ICO）。这意味着本地 `AGENTS.md` 中"`docs/site/assets/*.svg` 由 `scripts/gen-logo.sh` 生成，`--check` 是 CI 门禁（`ci.yml` vet job 与 `release.yml` brand job）"整段**已失效**。
2. **品牌门禁从 CI 中消失**：远端 `ci.yml` 的 vet job 已更名为 `vet + gofmt`，`gen-logo.sh --check` 步骤不存在；`release.yml` 亦无 brand job。远端另新增 `windows-compile-gate` job（交叉编译 Windows 测试二进制 + 跨 OS vet）。
3. **远端 `.gitignore` 未忽略 `.DS_Store`**：本地新加的 `.DS_Store` 规则在合并后仍有效（远端未删除该文件），但需注意远端已重排该文件并新增 `.rustcode/*`、`zcode2api/`、`.superpowers/`、`/.mcp.json` 等条目，合并时应保留远端规则。
4. **远端新增 `AGENTS.md`/`README.md`/`TODO.md` 大量改写**（`git diff --numstat`：README `+30/-51`、AGENTS.md `+25/-27`、TODO.md `+38`）。本轮对这三个文件的文档更正与之重叠面大，**很可能产生冲突**，应以远端版本为基准重新施加，而非直接套用本地补丁。

## 验证状态（实测）

最近一次全量复核：`2026-10-04`，macOS arm64（`go1.27.1 darwin/arm64`）。

| 检查项 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build` | 通过 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` | 通过 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build` | 通过 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build` | 通过 |
| `bash scripts/build.sh windows amd64` → `file` 检查 | `PE32+ executable (GUI)`（`-H windowsgui` 生效） |
| `go vet ./...` | 通过 |
| `gofmt -l .`（排除 `reference/`） | 无输出 |
| `go test ./...` | 297 个顶层用例；除下述 2 个环境受限用例外全部通过 |
| `bash scripts/check-emoji.sh` | 通过（未发现 emoji） |
| `bash scripts/gen-logo.sh --check` | 通过（品牌 SVG 与几何一致）— ⚠️ 仅对**本地** `main` 成立；远端已删除该脚本，详见「远端状态」 |

**环境受限用例（非代码缺陷）**：`internal/server.TestHandleIndexServesEmbeddedPanel` 与
`modules/updater.TestFetchLatest` 依赖 `httptest.NewServer` 监听回环端口，在禁止监听套接字的
受限沙箱中会 panic；`modules/preferences` 的用例走 `app.New()` 读配置目录，设置
`PCMANNAGER_CONFIG` 指向可写目录后通过。CI（ubuntu runner，无上述限制）中均正常运行。

四平台产物命名：`pcmannager-<os>-<arch>[.exe]`。
