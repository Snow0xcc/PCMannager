# PCMannager 交接文档

> **最近更新：2026-10-04**（ROADMAP 首轮 A→F + 后续 Task 1–4 全部执行完毕，
> 最新发布 `v0.1.8`）。计划单一数据源见 [`ROADMAP.md`](ROADMAP.md)，
> 待办与优先级见 [`../TODO.md`](../TODO.md)。

## 1. 项目状态

项目已完成**架构重构 + 面板产品补齐 + 发布流水线**三块工作，进入
"功能已补齐、待真实 Windows 实机验收"阶段。

### 已完成的关键阶段

- **架构重构（阶段 A–F，2026-10-03 完成）**：统一模块到 `internal/core.Module` 契约；
  拆解旧 `core.Registry`/`core.App`；`internal/app`/`server`/`config` 集成；
  热键线程模型、托盘锁协议、事件总线竞态三个 **P0 运行时阻断项**已修复
- **发布流水线（TODO #1–#7）**：`scripts/build.sh` 统一构建（`CGO_ENABLED=0 -tags production -H windowsgui`）、
  版本号构建注入、`auto-release.yml` 自动递增打 tag + 派发 + Wiki 同步、
  `release.yml` 四平台构建发布、`modules/updater` 自动更新模块、
  `docs/RELEASE-CHECKLIST.md` 发布核对清单
- **面板产品补齐（ROADMAP C2，5 项全部完成）**：剪贴板/截图/上下文条目列表面板化、
  热键冲突可视化、需重启标记、日志页过滤与导出、theme 开关接线
- **本轮 Task 1–4（2026-10-04）**：
  - Task 1–3：`docs/RELEASE-CHECKLIST.md` + 台账建立
  - **Task 4 / TODO #9**：五档品牌 `.ico`（16/32/48/64/256 PNG-in-ICO，
    `internal/tray/assets/icon.ico`）+ 四级回退链（`app.tray_icon_path` → 内嵌 →
    程序化蝴蝶 → shell）+ 面板配置项
  - **TODO #12/#13**：模块命名与平台成对对账（三项检查 0 不一致）
  - **TODO #11**：`docs/NAMING-MIGRATION.md` GoBox→PCMannager 迁移设计（只设计不执行）
- **最终全分支审查（`ca275c2..dbb64d6`）**：2 轮闭合，CRITICAL 0 / MAJOR 2（已修）/
  MINOR 6（已修）/ round 2 REGRESSION 1（已修）；含变异测试与资产可复现校验

### 三个 P0 运行时阻断项

| 项 | 状态 | 备注 |
| --- | --- | --- |
| P0-1 托盘同锁重入死锁 | 已修复 + 测试 | 锁协议"锁内快照、锁外通知"；**未做真实 Windows 实机验证** |
| P0-2 热键注册线程错误 | 已修复 + fake backend 测试 | 注册/注销移到泵线程执行；**未做真实 Windows 热键触发验证** |
| P0-3 Bus 发送/关闭竞态 | 已修复 + 压测 | 锁序恒为 `Bus.mu → subscriber.mu`；历史在锁内同步预填 |

"架构已稳定"仅指模块契约与依赖方向稳定；**运行时行为仍未经真实 Windows 验收**，
详见 [`PROJECT-AUDIT.md`](PROJECT-AUDIT.md) 第 6.1 节与本文件第 6 节清单。

---

## 2. 已验证的命令

以下命令在提交 `dbb64d6` 上实际执行并全部通过（**Linux 环境**）：

```bash
cd /workspaces/PCMannager

gofmt -l . | grep -v ^reference/   # 无输出
go build ./...                      # OK
go vet ./...                        # OK
GOOS=windows go vet ./...           # OK（Windows 专属代码的类型检查闸门）
bash scripts/check-emoji.sh         # 通过（含探测器自检）
go mod tidy -diff                   # OK（无漂移）
go test -count=1 ./...              # 18 包全 ok，372 用例
```

**发布产物构建必须走脚本**（勿直接 `go build`，否则丢 `-H windowsgui` 弹黑框、
丢 `-tags production` 静默退出）：

```bash
bash scripts/build.sh <goos> <goarch> <输出>
```

验证结果摘要：
- 用例总数以**实测**为准：`grep -rn "^func Test" --include=*_test.go internal/ modules/ | wc -l`
  （**当前 372**；README/TODO/AGENTS 曾另写 175/202/210，均已失效——不要手写用例数）
- `GOOS=windows` 侧逻辑本机**只能静态把关**（vet 类型检查 + 审查），无法实跑
- `-race` 不可用（需 cgo，与 `CGO_ENABLED=0` 约束冲突）

---

## 3. 关键技术架构

### 核心契约
- [internal/core/module.go](../internal/core/module.go)
- 设计目标：统一模块能力，所有功能都实现 `core.Module`

### 应用编排
- [internal/app/app.go](../internal/app/app.go)
- [internal/app/provider.go](../internal/app/provider.go)
- 负责注册模块、热键、事件总线、托盘与生命周期管理

### HTTP / 首选项面板
- [internal/server/server.go](../internal/server/server.go)
- [internal/server/provider.go](../internal/server/provider.go)
- 提供 REST + SSE 接口，给前端或设置面板使用

### 配置管理
- [internal/config/config.go](../internal/config/config.go)
- 当前使用 YAML 配置，支持原子写入和默认值合并

### 模块目录（8 个包，其中 7 个在 `main.go` 经 `MustRegister` 注册）

- [modules/taskbar](../modules/taskbar) — 任务栏状态（CPU/内存/网络/磁盘/电量）
- [modules/clipboard](../modules/clipboard) — 剪贴板历史
- [modules/screenshot](../modules/screenshot) — 截图/录屏/长截图/编辑器
- [modules/selfcontext](../modules/selfcontext) — 上下文记录
- [modules/repair](../modules/repair) — 修复/工具箱（声明式 `catalog.go`）
- [modules/launcher](../modules/launcher) — 超级面板/应用启动器
- [modules/updater](../modules/updater) — 自动更新（默认关闭）
- [modules/preferences](../modules/preferences) — **不是注册模块**，是注册表视图与
  Manager 封装（`main.go` 不注册它）

### 支撑包

- [internal/core](../internal/core) — `Module` 契约、`Bus`、`HotkeyManager`、`Option`/`Action`
- [internal/winui](../internal/winui) — 无 cgo 的 Win32 封装（`_windows`/`_other` 成对）
- [internal/tray](../internal/tray) — 托盘抽象 + 图标加载链（`icon.go` 中立 / `icon_windows.go` loader）
- [internal/panel](../internal/panel) — 首选项面板**唯一前端资源**（`index.html`，`embed` 内置）
- [internal/wailsapp](../internal/wailsapp) — 原生窗口层（Windows 用 Wails，与 `server` 并存）
- [internal/config](../internal/config) / [internal/paths](../internal/paths) /
  [internal/logx](../internal/logx) / [internal/sysutil](../internal/sysutil) — 配置、路径、日志、系统工具

---

## 4. 当前实现重点

### 任务栏状态模块
- 位置： [modules/taskbar/feature.go](../modules/taskbar/feature.go)
- 目标：TrafficMonitor 风格的任务栏状态统计
- 特点：系统信息采集与窗口展现分离，具备跨平台降级能力

### 电脑修复模块
- 位置： [modules/repair/feature.go](../modules/repair/feature.go)
- 位置： [modules/repair/catalog.go](../modules/repair/catalog.go)
- 目标：声明式修复/工具安装入口，按类别组织修复动作

### 剪贴板模块
- 位置： [modules/clipboard/feature.go](../modules/clipboard/feature.go)
- 目标：Ditto 风格的剪贴板历史管理
- 设计：支持历史记录、回写、重复去重与跨平台降级

### 截图模块
- 位置： [modules/screenshot/feature.go](../modules/screenshot/feature.go)
- 目标：Snipaste 风格的截图与编辑流程

### 上下文记录模块
- 位置： [modules/selfcontext/feature.go](../modules/selfcontext/feature.go)
- 目标：记录当前窗口上下文/工作状态，支持回看与摘要

### 首选项面板
- 位置： [modules/preferences/manager.go](../modules/preferences/manager.go)
- 目标：统一配置入口、热键管理和模块开关控制

### 启动器模块
- 位置： [modules/launcher/feature.go](../modules/launcher/feature.go)
- 目标：uTools 式超级面板/应用启动器（拼音首字母缩写、多因子加权排序与置顶）

### 自动更新模块（默认关闭）
- 位置： [modules/updater/feature.go](../modules/updater/feature.go)
- 目标：检查 GitHub Releases、下载（主机白名单 + sha256）、提权替换与重启
- **绝不自动执行**下载物；`apply_update` 经 `sysutil.RunElevated` 跑 PowerShell helper

---

## 5. 现状中的风险 / 待收口事项

以下事项仍建议接手人继续检查：

1. 运行时 UI 细节
   - 托盘图标（TODO #9 本轮刚交付，**从未在真实 Windows 上看过**，见 6.7）
   - 任务栏嵌入窗口的尺寸与定位
   - 截图编辑器的交互细节

2. 真实 Windows 环境测试
   - 需要在 Windows 环境下实际执行热键、截图、复制、任务栏窗口等功能
   - ROADMAP **B5 真实 Windows 验收**是当前最优先的未完成项（`TODO.md` 首项）

3. 交互与日志一致性
   - 面板展示与真实模块状态需要再做一轮对齐校验

4. 细节稳定性
   - 升级/降级分支、异常恢复、后台 goroutine 生命周期

5. **待你裁定的观察项**（本轮执行者提交，尚未决定）
   - **Release 膨胀**：`auto-release.yml` 每次 push 到 main 都自动打 tag + 发 Release，
     本轮 3 次 push 产生 3 个 Release（v0.1.6/7/8）。是否改攒批/条件/手动触发？
   - **孤儿 tag**：tag 已推而 `gh workflow run` 瞬时失败时该 tag 永无 Release
     （v0.1.1/v0.1.2 同型），且**无补发机制**。
   - **真实用户配置的既有测试污染**：根因已修（`newTestApp` 覆盖 `HOME`/
     `XDG_CONFIG_HOME`，两处），但 `~/.config/GoBox/config.yaml` 里残留着既有
     测试累积的模块（`a/b/both/c/d/hkbad/hkm/orphan/racey/restarty/rt/x/y/z`），
     未擅自删除（是本机用户文件）。要清理请手工删或授权。
   - **runner 告警（外部）**：`actions/checkout@v4` Node 20 弃用；
     `ubuntu-latest` → Ubuntu 26 迁移于 **2026-10-19**。
   - **gh 令牌缺 `actions:write`**：无法手动 dispatch（403），补发机制即便做了也跑不了。

6. **deferred minors（未修，可顺手）**
   - `auto-release.yml` 派发步骤无失败重试/补发机制（同上"孤儿 tag"）
   - 头注释已修正，但派发步骤的时序竞态（tag push 与 workflow_dispatch 并发）
     未实测验证

---

## 6. 接手建议：Windows 实机验收清单

截至 `dbb64d6`（`v0.1.8`），Linux 下已验证：四平台 `CGO_ENABLED=0` 构建、
`gofmt`/`go vet`/`GOOS=windows go vet`、emoji 检查、`go mod tidy -diff`、
**372 用例**全绿。但**以下各项只能在真实 Windows 上确认**，当前尚未验证。

产物构建（与 CI 同源，勿直接用 `go build`，否则丢 `-H windowsgui`）：

```
PCM_VERSION=$(git describe --tags --abbrev=0) bash scripts/build.sh windows amd64 dist/pcmannager-windows-amd64.exe
```

### 6.1 黑框是否消失（回归验证）

双击 exe 启动，观察是否弹出黑色控制台窗口。

- 预期：无任何控制台窗口，只有托盘图标。
- 判据：`file dist/pcmannager-windows-amd64.exe` 应为 `PE32+ executable (GUI)`。
  若为 `console`，说明 `-H windowsgui` 丢失。
- 注意：GUI 子系统下 stdout/stderr **不可见**，排障请查日志文件或面板
  “事件日志”页——这是本轮 `last_error` 要解决的正是这个问题。

### 6.2 Wails 原生窗口（新增，最高风险）

托盘菜单点击“打开面板”（`open_panel`）。

- 若 `app.open_in_webview = true` 且 WebView2 运行时存在：应弹出独立原生窗口。
- 若 WebView2 运行时缺失：应**静默回退**到系统浏览器打开 HTTP 面板，
  并写 WARN 日志。回退正常不算失败。
- 判据：窗口内应能看到模块列表、支持搜索、能改配置并保存成功。
- 风险点：窗口在独立 `LockOSThread` 线程创建（托盘泵必须留在主线程），
  若出现“点了没反应”，查日志中的 `wailsapp` 相关行。

### 6.3 任务栏小组件嵌入

启用 taskbar 模块后观察任务栏是否出现 CPU/内存/网络/磁盘读数。

- 预期：嵌入到 `Shell_TrayWnd` 内，位于系统托盘区左侧。
- 若找不到任务栏（如 Explorer 未运行）：应降级为悬浮窗并报 `errNoTaskbar`，
  不应崩溃。
- 额外验证：重启 Explorer 后小组件是否重新嵌入。

### 6.4 repair 提权与危险动作

- 触发标 `Admin` 的条目（如 .NET 安装、网络重置）：应弹出 UAC 提权框。
- 已是管理员时应直接执行，不再弹框。
- 触发 `Danger` 条目：面板需二次确认（红条 + 再点一次）。
- 判据：命令确实执行且结果写入事件日志。

### 6.5 自动更新（updater 模块，默认关闭）

面板开启 `自动更新` 模块后逐项验证（Linux 已实测 check/download 前置链路，
**apply 全链路只能在 Windows 实机验证**）：

- `check_now`：state 显示 `latest` 与匹配的平台资产名（如
  `pcmannager-windows-amd64.exe`），事件日志出现"已是最新版本/发现新版本"。
- `download`：更新包落到 `DataDir/pcmannager.update`，state.staged 出现该路径。
- `apply_update`（danger+admin）：应弹 UAC → 等待本进程退出 → 备份 exe 为
  `.bak` → 替换 → 自动重启；重启后 `check_now` 应显示 `update_available=false`。
- 失败恢复点：若替换中断，`.bak` 即旧版本，可手工改回。

### 6.6 热键

- 默认 `F1`（截图）、`Ctrl+\``（剪贴板）、`Ctrl+Alt+T`（任务栏）是否触发。
- 若被其它程序占用：面板该模块页应显示红色告警条说明原因，并提供
  “修改热键”按钮一键跳到输入框——**这是本轮新增能力**。
- 改绑为其它组合后应立即生效，不需重启应用。

### 6.7 托盘图标（本轮新增，TODO #9）

四级回退链：`app.tray_icon_path` → 内嵌 `internal/tray/assets/icon.ico` →
程序化蝴蝶标 → shell 通用图标。

- 默认（`tray_icon_path` 留空）：托盘应显示**蝴蝶品牌标**，而非通用方块图标。
  判据：`icon_windows.go` 的 `iconHandle()` 命中 `embedded` 源。
- 面板"应用设置"填入自定义 `.ico` 路径 → 保存 → 提示需重启 → 重启后托盘显示该图标。
- 填入**不存在/损坏**的路径 → 应写 WARN 日志（`自定义托盘图标加载失败，已回退`）
  并回退到内嵌图标，**不崩溃**。GUI 子系统无控制台，此日志只能从日志文件看。
- 观察点：用户自定义 .ico 的**真实显示效果与 DPI 缩放观感**（125%/150% 缩放下
  是否清晰）——本机 Linux 无法验证。
- 回归：Hide/Show（勾选"显示图标"再取消再勾选）循环多次，任务管理器内存
  不应持续增长（`sync.Once` 缓存 HICON 是为此）。

### 6.8 回归项

托盘启退循环、剪贴板历史记录与写回、截图编辑器、selfcontext 采集开关
（默认关闭，启用前应看到隐私说明块）、面板条目列表页（剪贴板/截图/上下文）。

---

## 7. 结语

架构重构、面板产品补齐、发布流水线三块已完成，最新发布 `v0.1.8`。
**下一步最优先的是真实 Windows 实机验收（ROADMAP B5）**，而不是继续底层重构——
本轮交付的托盘图标（TODO #9）与此前的托盘/热键/任务栏/Wails 改动都只有
`GOOS=windows go vet` 静态把关，一次实机 smoke test 能一次性覆盖第 6 节全部条目。

接手顺序建议：

1. 先读 [`ROADMAP.md`](ROADMAP.md) 的「执行顺序建议」与「明确不做」清单——
   计划的单一数据源，本文件只反映状态不承载计划
2. 读 [`../TODO.md`](../TODO.md) 确认优先级（第 1 项即 B5 实机验收）
3. 跑第 2 节命令确认基线，再按第 6 节清单在 Windows 上逐项验收
4. 第 5 节第 5 条的**待裁定观察项**需要维护者决策后才能动发布流程

注意 AGENTS.md 的硬约束：平台成对文件、三大锁协议（托盘/热键/事件总线）、
Win32 DLL 归属、前端禁 emoji、发布必走 `scripts/build.sh`、测试三坑。
改文档时同步 AGENTS.md 的维护规则——它记录的结构事实必须与代码同步。
