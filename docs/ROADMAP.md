# ROADMAP — 后续开发与推进方案

> 更新日期：2026-10-30。基线：`v0.1.12`（最新 Release，四平台产物齐全，CI 全绿）。
> 本轮验证：全量测试、`preflight windows amd64 --with-tests`（跨目标只编译不执行）、
> emoji 闸门及四个发布目标构建通过；当前测试函数数以现场统计为准（本轮 **423**）。

## 0. 当前判断（一句话）

代码、测试与跨平台构建基线目前健康，阶段 A–D 的主要代码整改已完成。
现阶段的首要风险不是缺少功能，而是**Windows 运行时尚缺真实机器验收证据**；
其次是收敛仍存在的已知体验/发布待办。应先完成实机验收和既有待办，不建议继续横向增加模块。

### 0.1 第 0 阶段收尾（已完成，2026-10-30）

- **剪贴板历史可选持久化**：`persist_history`（默认关闭）。文本与条目索引写入
  `clipboard_history.json`（`0600`、目录 `0700`、64 MiB 上限、版本字段、图片缓存路径校验），
  恢复后 ID 继续递增、按保留期/上限裁剪；关闭开关删除文件。
- **写入防抖**：连续变更在 300ms 窗口内合并为一次全量重写；`Stop` 与启动边界强制 flush，
  关闭持久化时取消待写定时器，避免删除后又被重建。
- **Windows 替换兜底**：`os.Rename` 失败（目标被占用）回退直接覆盖写，不丢本次历史。
- **边界处理**：损坏/未知版本历史文件只告警不中断启动；图片缓存缺失的条目在恢复时跳过。
- **工具链**：`scripts/preflight.sh --with-tests` 在目标平台 != 宿主时改为逐包
  `go test -c`（不执行），修复 Linux 上验 Windows 必现的 `exec format error`。
- **文档**：ROADMAP/HANDOVER/TODO/README/CHANGELOG/发版清单同步到 `v0.1.12` 实况；
  面板截图入库。

下一步仍是 B5 实机验收；发布流程治理（孤儿 tag、自动发版节奏）待维护者裁定。

---

## 阶段 A — P0 止损：已发布但不可用的功能（建议立即做，1–2 天）

> **状态（2026-10-03）：A1–A7 已全部完成**，分支 `fix/stage-a`（基于 `13c8f0c`），
> 每项独立提交、TDD 验收；`go test -race -count=1` 全绿、四平台交叉编译通过。
> A1–A7 的改动点：A1 `internal/panel`+`internal/server`；A2 `internal/app/version.go`；
> A3/A4 `modules/updater/feature.go`；A5 `scripts/check-emoji.sh`；A6 `internal/panel/index.html`；
> A7 `internal/config/config.go`+`internal/server/provider.go`+`internal/app/provider.go`。
> **同日追加**：评审修复关（I-1 竞态 + M-1~M-5）与 B4（windows-compile-gate）、
> F（文档校准）、B1（面板四层安全闸门）、B2（repair 执行预算 + walk 提权/异步）、
> B3（剪贴板图片字节上限）亦已完成；M-6/M-7 记账延后。
> **追加（远程合并后）**：C4（4 处并发生命周期）、C3（selfcontext/screenshot 持久化）、
> C1（死代码净删 696 行，含按合并后基线划除的过时项）、C2-1（面板剪贴板历史浏览）已完成。

这一阶段的共同特征：**代码「已实现」且被文档标记为完成，但实际不生效**。
这是当前最高价值的投入——修的都是既有投入的兑现，不是新增功能。

### A1. 面板事件流在 HTTP 路径上完全失效 【最高优先】

- **证据**：`internal/server/server.go:312` 写出 `event: <type>\ndata: `，
  而 `internal/panel/index.html:314` 只注册 `es.onmessage`。
  按 SSE 规范 `onmessage` 仅对**无名事件**触发，故 `log`/`state`/`progress`/`notice`
  四类事件（含 Bus 的 200 条历史回放）**一条都进不了面板**。
  Wails 路径（`index.html:321`）用 `runtime.EventsOn`，不受影响。
- **叠加缺陷**：`index.html:909` 的 `pushLog` 在日志页未挂载时直接 no-op，
  即便修好 SSE，切到别的页签后仍收不到日志。
- **影响**：HTTP 面板是**非 Windows 平台唯一控制面**，其「事件日志 / 进度 / 通知」三页是空的。
  `internal/server/server_test.go:339` 只 grep 裸 `data:` 行，结构上无法发现该缺陷。
- **改动**：二选一——(a) 服务端去掉 `event:` 字段，只发无名事件；(b) 前端按类型
  `addEventListener`。建议 (b)（保留类型信息，前端 `onEvent` 已按 `type` 分发）。
  同时给 `pushLog` 加环形缓冲，页签切换不丢日志。
- **验收**：`go test` 中新增一条**浏览器无关**的契约测试——断言服务端写出的
  `event:` 名与前端 `addEventListener` 注册名集合一致（用共享常量表驱动，杜绝再次漂移）。

### A2. 面板版本号显示为 `vv1.2.3`

- **证据**：`index.html:344` 为 `"v" + state.version`，而
  `scripts/build.sh` 注入的 `PCM_VERSION` 在 CI 中等于 `github.ref_name`（已带 `v`）。
- **改动**：`app.Version` 归一化去掉前导 `v`（`internal/app/version.go`），或前端不再补 `v`。
  建议后端归一化——版本号是数据，不该由展示层拼接。
- **验收**：`internal/app` 加一条 `Version` 归一化单测。

### A3. 自动更新在发布构建上恒为「有新版」（真 bug，非未验证）

- **证据**：`modules/updater/feature.go:177` 的 `currentVersion()` **只读 `debug.ReadBuildInfo()`**，
  完全忽略 ldflags stamp，发布构建下返回 `0.0.0-dev`；
  于是 `isNewer()`（`:258`）把**当前 tag 本身**判为更新 →
  永久误报「有新版本」+ 反复下载同一个二进制。
- **注**：`feature.go:174` 的注释说「直接读 app 包会反转依赖，故故意复制逻辑」——
  但 `core.Context.App`（`internal/core/module.go:149`）**已经提供 `AppControl.Version()`**，
  这个 seam 就是为此而设，复制逻辑是不必要的。
- **改动**：`currentVersion()` 改为 `f.ctx.App.Version()`，删除重复的 build-info 解析。
- **验收**：`modules/updater` 加单测：给定 stamp 版本，断言 `isNewer` 对同版本返回 false。

### A4. 模块关闭时仍在向 GitHub 轮询

- **证据**：`modules/updater/feature.go:121` 在 **`Init`** 里 `go f.loop()`，
  门控只有 `auto_check`，**不看 `Enabled()`**；而 `app` 在 `Init` 之后才按开关决定 `Start`。
- **影响**：默认关闭的模块在启动 60 秒后仍发起网络请求，与文档「默认模块关闭」的承诺矛盾；
  企业/离线环境下是无谓的出网尝试。
- **改动**：把 loop 移到 `Start()`、在 `Stop()` 取消（当前 `Init` 起的 goroutine 无人 join）。
- **验收**：单测断言 `Enabled=false` 时不产生任何 HTTP 请求（用注入的 `httpClient` 计数）。

### A5. emoji 硬约束闸门形同虚设（实测确认）

- **证据**：`scripts/check-emoji.sh` 在 `TODO.md`（含 U+2705 ✅、U+2192 →）上 **exit 0**。
  对照实验：`grep -Pl '\x{2705}' TODO.md` **匹配**，
  而脚本用 bash `$'\U0001F000'` 拼出的**裸 UTF-8 字节**区间串 `grep -P` **不匹配**。
  即闸门从不失败。
- **附带设计问题**：脚本扫描 `*.md`，且区间含 `\x{2190}-\x{21FF}`（→←）——
  这些是**正常文档排版字符**。即使把正则修对，闸门也会立刻被自家文档卡死
  （`AGENTS.md`、`CHANGELOG.md`、`docs/*` 均含）。
- **改动**：正则改用单引号 `'\x{1F000}-\x{1FAFF}...'` 形式（实测可用）；
  **扫描范围收敛为前端资产**（`*.html/*.css/*.js`，即「客户端界面」的真实定义），
  符号区段（`2190-21FF`、`2B00-2BFF`、`FE0F`）仅保留在 `*.html` 上。
- **验收**：加一条自检——脚本内建一个**必须失败**的临时 fixture，失败才算闸门有效。
  没有自检的检查器等于没有检查器。
- **另**：`check-emoji.sh:15` 的 `./reference/*` 前缀判断是死代码，`git ls-files` 不输出 `./` 前缀。

### A6. 面板 XSS（未转义插值）

- **证据**：`index.html:455` 把 `a.effective_data_dir` 直接拼进 `el()` 的 `innerHTML`，
  全站唯一一处未走 `esc()`（对比 `:487/521/567/672/736/758/840/874` 均已转义）。
- **改动**：走 `esc()`。数据目录来自配置，本地可控，但这是唯一破口，值得一并收口。

### A7. 配置读写竞态 + `server_port` 静默失效

- **证据**：`internal/config/config.go:206` 的 `Config()` **直接返回活文档指针** `m.cfg`；
  `internal/app/app.go` 有 5 处未持锁直读（`:92/108/673/779/846`），
  与 `UpdateApp` 的持锁写（`config.go:356`）并发。
- **叠加产品缺陷**：`server_port` 只在 `StartPanel` 读一次（`app.go:673`），
  面板改端口**保存成功但无任何效果，且没有 `Restart: true` 标记**——用户无从得知需重启。
- **改动**：(a) `Config()` 返回快照/只读访问器，杜绝裸指针外泄；
  (b) `server_port`/`data_dir`/`log_level` 标注为需重启（复用已存在的 `core.Option.Restart` 机制，
  **不要**再引入 `[restart]` 前缀 hack——TODO #18 刚把它拆掉）。
- **验收**：`go test -race` 下并发 PATCH + 读，覆盖上述路径。

---

## 阶段 B — P0/P1 质量闸门与安全（3–5 天）

### B1. HTTP 面板无鉴权、无 CSRF 防护（安全）

- **现状**：`internal/server` 仅绑 `127.0.0.1`（`server.go:47`）+ 随机端口，**除此之外零防护**：
  无 token、无 Origin/Host 校验、无 `Sec-Fetch-Site` 检查。
- **影响面**：任何本机进程可完整读写配置，并可经
  `POST /api/modules/repair/actions/{action}` **执行任意命令行**、经 `sysutil.RunElevated` **弹 UAC**。
  该路由空 body 时属于 CORS 简单请求，`mode:'no-cors'` 即可从**任意网页**触发（无预检）。
- **改动**（按性价比排序）：
  1. 启动时生成随机 token，放 URL query，**所有变更类路由校验** `X-PCMT-Token` 或 query；
  2. 变更类路由要求 `Content-Type: application/json`（直接挡掉全部简单请求）；
  3. 拒绝 `Sec-Fetch-Site: cross-site`；
  4. 校验 `Host` 头为 loopback。
- **验收**：`internal/server` 加 httptest 用例：缺 token / 跨站头 / 简单请求均 403。

### B2. repair 执行无超时，且 walk 面板绕过提权

- **证据 1**：`modules/repair/exec_windows.go:13` 用 `exec.Command("cmd","/c",...)` +
  `CombinedOutput()`，**无 `CommandContext`、无超时**。`disk_cleanup`/`defrag`/`ipconfig /all`
  一旦挂起即永久阻塞。
- **证据 2**：`modules/repair/panel_windows.go:151` 直接调 `runCommand(cmdline)`，
  **绕过 `Feature.RunAction`**（`feature.go:110` 才有提权路径）→
  目录中 21 个 `Admin: true` 条目在 walk 窗口里**静默以非提权方式失败**。
- **证据 3**：该调用在 walk 窗口的 **UI 线程**上同步执行 → 面板直接冻住。
- **改动**：`RunAction` 内改 `exec.CommandContext` + 超时；walk 面板改为异步调用
  `Feature.RunAction`（走同一条提权路径），UI 线程只负责发起与收尾。
- **验收**：`modules/repair` 加单测覆盖 `RunAction` 的超时与提权分派。

### B3. 剪贴板图片无字节上限（OOM 风险）

- **证据**：`modules/clipboard/feature.go:329` `ingest` 存原始 PNG，**无单条字节上限**；
  `store_images` 默认开、`max_items` 上限 5000，`echoImage`（`:387`）还留**第二份完整副本**。
- **改动**：单条超阈值（如 8 MiB）拒收或转存文件只留引用；总容量上限。
- **验收**：单测构造超大条目，断言被拒且历史长度不变。

### B4. Windows 代码在 CI 中从未被编译为测试代码（关键盲区）

- **证据**：`.github/workflows/ci.yml` 中 `go test`（:45）与 `go vet`（:96）均跑在 ubuntu，
  `GOOS` 只在 build 矩阵（:64）设置。故 `_test.go` 的 **Windows build tag 文件
  （如 `internal/tray/tray_windows_test.go`）在 CI 中一次都没编译过**，
  `hotkey_windows.go`（303 行 P0-2 线程亲和逻辑）、`winui/*`（1189 行）**零 CI 验证**。
- **加重因素**：P0-1（托盘死锁）与 P0-2（热键线程）**都是 Windows 专属 bug**，
  而它们恰恰是这套闸门覆盖不到的部分。
- **改动**：CI 增 job——`GOOS=windows go vet ./...` + `go test -c -o /dev/null ./...`
  （把每个测试包编译成 Windows 二进制，**不执行**）；另加 `GOOS=darwin/linux` 的 vet。
- **验收**：故意在 `tray_windows_test.go` 引入语法错误，CI 必须红。
- **顺带**：补 `staticcheck`/`golangci-lint`、恢复 `go mod tidy -diff` 闸门、加 Dependabot；
  `test` job 无需 checkout submodules（`reference/` 不参与构建）。

### B5. 真实 Windows 实机验收（无法在本环境完成，必须排期）

> **状态（2026-10-04）**：可勾选验收清单已建——`docs/RELEASE-CHECKLIST.md`
> （机械发版步骤 + 2.1–2.8 实机验收节 + 发版记录模板）；**实机执行仍为外部依赖**，
> 执行后按模板留档才算完成本项。

当前环境为 Linux，以下全部**只有代码与自动化测试，没有实机证据**：

- 托盘连续启退 20 次无残留窗口；真实 Explorer 重启后恢复
- 全局热键真实触发（P0-2 线程模型）
- 任务栏小组件嵌入 `Shell_TrayWnd` 正常显示（TODO #20 的修复未验证）
- `updater` 的 `apply_update` 全链路（等进程退出 → 备份 → 替换 → 重启）
- Wails/WebView2 窗口渲染与交互（缺运行时须正确回退浏览器）
- repair Walk 面板 tooltip 初始化（`TTM_ADDTOOL failed`；代码已加 `LockOSThread`，需实机确认）
- 热键冲突真实复现（TODO #21：`F1`、`Ctrl+\`` 被占用属环境冲突，设计上已降级）

**建议**：写一份可勾选的验收清单（`docs/RELEASE-CHECKLIST.md`），
每次发版前逐项打勾并记录机器/系统版本。这是目前**最大的未量化风险**——
代码正确性在 Linux 上无法证伪。

---

## 阶段 C — P1 产品补齐（1–2 周）

### C1. 死代码清理（低风险、立刻提升信噪比）

以下均为**零引用**（仅被自身测试引用或完全无引用），已逐个核实：

| 目标 | 位置 | 说明 |
|---|---|---|
| `core.Registry` | `internal/core/registry.go` | 整个包无人使用，`app.App` 自带一套等价实现；**5 个测试在守护死代码** |
| `core.VisibleIf` | `internal/core/module.go:45` | 2 个模块声明（`screenshot:101`、`taskbar:176`），**面板与 walk 面板都不消费** |
| `preferences.Show` | `modules/preferences/panel_windows.go:18` | **零调用者**，285 行 walk 首选项窗口完全孤立；`main.go:45` 只在注释里提到 |
| `sysutil.AcquireSingleInstance` | `sysutil_{windows,other}.go` | 两平台都有实现，**无任何调用者** → README 宣称的「单实例」根本没接线 |
| taskbar `actionCopy` | `modules/taskbar/feature.go:92` | 常量已声明，**既不在 `Actions()` 也不在 `RunAction`** |
| 5 个惰性 option | `modules/taskbar/feature.go:148/154/177/185/191` | `layout`/`num_align`/`follow_theme`/`render`/`multi_monitor` 在面板可见，但 `widget_windows.go` **一个都不读** |
| `logx.busHandler.groups` | `internal/logx/logx.go:147,189` | 只写不读 |
| ~20 个 winui/wailsapp 导出 | `internal/winui/*`、`wailsapp/binding.go:45` | 无引用（含仅存在于 `_other.go`、Windows 侧无对应实现的 `Elevate`/`IsElevated`） |

- **重点提醒**：惰性 option 属于**用户可见的假设置**——面板上能改，改了没反应。
  要么实现，要么从 `Options()` 摘掉，二选一，不能留着骗人。

### C2. 面板缺失的关键能力

> **状态（2026-10-04）**：**5 项均已完成**。第 3 项（需重启标记）经核实**代码早已接线**
> ——`internal/app/provider.go:142` 以 `restartRequiredKeys` 暴露 `restart_required`
> DTO，`index.html:613` 消费并提示"重启应用后生效"，`server_test.go:348` 有契约测试；
> 机制说明：这三项是应用级设置而非模块 option，故用应用级等价物（DTO 列表）而非
> `core.Option.Restart`。第 5 项（C2-5）已于 `caa368a` 落地（截图历史 + 上下文记录
> 条目列表面板化，三模块 `State()` 暴露 entries + REST 透传 + 面板页）。

按用户价值排序（**下列缺口描述是立项时的原始问题**，供追溯；括号内为落地位置）：

1. **剪贴板历史浏览**（原最大缺口）：立项时 `State()` 只暴露 `count/last/last_kind`、
   不返回条目，非 Windows 用户无法查看或写回历史 —— **已修**：`State()` 现返回
   `entries: panelEntryViews(...)`（`clipboard/feature.go:225`）+ 面板条目列表页。
2. **热键冲突可视化** —— **已修**：`panelProvider.Conflicts()`（`app/provider.go:196`）
   经 `/api/state` 顶层 `conflicts` 键透出（`server/server.go:292`），面板
   `index.html:629` 渲染冲突行。
   热键**可用性检测 + 改绑引导**另见 TODO #21，已由保存前试注册与面板按键录制实现。
3. **需重启标记**：`server_port`/`data_dir`/`log_level`（见 A7）—— **已修**：
   `restartRequiredKeys` 暴露 `restart_required` DTO，面板保存后提示"重启应用后生效"。
4. **日志页**：无过滤/清空/导出，`theme` 开关无消费者 —— **已修**：日志页有搜索过滤
   （`index.html:1421`）与 `#log-export` 导出 .log（`:1446`）；`theme` 由 `applyTheme()`
   消费（`:451`/`:611`），非假开关。
5. **截图历史 / selfcontext 条目列表**：立项时只能开原生窗口查看 —— **已修**
   （`caa368a`）：两模块 `State()` 暴露 entries + REST 透传 + 面板页。

### C3. 功能持久化缺陷

- `selfcontext` **从不持久化**：`feature.go:154` `Init` 里修剪的是一个空切片，
  历史**重启即全灭**——一个「上下文记录」模块没有记录。
- `screenshot` 的 `max_history` **只在会话内生效**（`feature.go:371` 每次启动从空 `f.saved` 开始），
  截图目录跨重启无界增长。

### C4. 并发与生命周期修复（4 项，均为小改动）

| 问题 | 位置 | 说明 |
|---|---|---|
| 热键 dispatcher goroutine 泄漏 | `core/hotkey.go:205-219` | `ch` 是局部变量，`Stop()` 从不关闭 → `for range ch` 永久阻塞，每次 Start/Stop 泄漏一个 |
| `Enable`/`Disable` check-then-act | `internal/app/app.go:305-340` | 先读 `Enabled() && Running()` 再 `Start()`；并发 HTTP PATCH + 托盘点击可重复 `Start` |
| `Window.Handle` 注册后写 | `tray_windows.go:135` vs `window_windows.go:412` | 窗口已进全局表后才赋值，且 `Window.mu` **存在但从未使用** |
| `wailsapp.cancel` 无同步 | `wailsapp/app_windows.go:193,209,220` | 跨 `OnStartup`/`OnShutdown`/`Run` 共享 |

---

## 阶段 D — P2 跨平台与依赖（1–2 周，可与 C 并行）

### D1. 跨平台真实成色（诚实盘点）

README 主打跨平台，但按代码逐模块核实，**非 Windows 上多数模块是残缺的**：

| 模块 | 非 Windows 实际可用度 | 关键缺口 |
|---|---|---|
| `taskbar` | ~85% | 仅小组件 no-op（`widget_other.go:33`），采集与面板正常 |
| `clipboard` | ~85% | 监视/历史正常；查看器 no-op，历史**不可浏览**（见 C2-1） |
| `screenshot` | **~55%** | **截图后直接丢弃**：`editor_other.go:16-22` 是 no-op，`onSave`/`onCopy` 永不触发 → 按 F1 抓屏后什么都没发生 |
| `selfcontext` | **~0%** | `capture_other.go:9` 恒返回空标题 → `feature.go:355` 直接 return |
| `repair` | 0% | 68 个 action 全部 `errUnsupported`（`exec_other.go:13`） |
| `updater` | ~95% | `applyUnix` 完整 |

- **决策点（需产品判断）**：是**继续做跨平台**（selfcontext 需 X11/Wayland portal，M 工作量），
  还是**明确收敛为 Windows 优先**并在 README 与面板中如实标注？
  当前「README 说跨平台、代码是 Windows 优先」的落差本身就是信任损耗。
  建议：**先在 README/面板如实标注成色**（S 工作量），再决定要不要投入做跨平台。

### D2. 依赖卫生（状态：**已完成，2026-10-30 复核**）

> **状态**：`kbinani/screenshot` 收进 `capture_windows.go`
> （`capture_other.go` 端 `captureDisplayCount()` 恒返回编译期常量 0，编辑器/录屏/
> 滚动拼接整棵调用树被死代码消除，非 Windows 二进制瘦身）；`gopsutil v4.26.9`
> 与 `x/crypto v0.57.0`、`x/net v0.59.0`、`x/sys v0.49.0`、`x/text v0.43.0` 已升级
> （越过 CVE-2025-22869 修复版本）。2026-10-30 本地合入 Dependabot 更新：
> `purego v0.11.2`、`dbus/v5 v5.2.4`、`wails/v2 v2.17.0`、`x/image v0.47.0`。
> 本轮验证：全量测试、Windows preflight、tidy/gofmt/emoji 闸门及四目标发布构建通过。

- `modules/screenshot/feature.go` **没有 build tag** → `kbinani/screenshot` 把
  `x/exp/shiny`、`x/mobile`、`x/image`、`xgb`、`plan9stats` 一并拖进 Linux/macOS 二进制。
  按平台打 tag 即可显著瘦身。
- `golang.org/x/crypto v0.33.0`、`x/net v0.35.0`、`x/sys v0.33.0`、`x/text v0.26.0`
  停留在 2025 年初，**早于 CVE-2025-22869**，需升级。
- `gopsutil v3.24.5`（v4 已发布）；`lxn/walk` 上游已停更（2021），仅 Windows 使用。
- 6 个直接依赖均在用，无冗余。

---

## 阶段 E — P3 架构收敛（可延后）

优先级最低，但值得记录，避免将来踩坑：

1. **`Provider` 是「传输层当契约层」**：`app -> server` 的依赖方向目前正确，但
   `wailsapp/binding.go:104-134` 直接绑定 **HTTP 的 DTO**，
   `server.Provider.Capabilities()` 返回 `any`（`provider.go:91`）——
   第二个传输层出现时契约必然分叉。建议把 DTO 抽到独立的 `panelapi` 包。
2. **`App` 是 god-struct**：22 字段 / 3 个互斥锁，横跨 config、bus、log、hotkey、tray、panel、
   HTTP server、autostart、single-instance（`app.go:32-80`）。
   `core.Module` 是 10 方法的 god-interface，且 `Base` 默认实现会**掩盖未实现的方法**。
3. **`modules/preferences/manager.go:11` 反向 import `internal/app`**，是唯一的层次倒置。

---

## 阶段 F — 文档校准（1 天，建议随 A/B 顺手做）

文档已明显漂移，会误导后续接手者，**建议一次性校准并改为机器校验**：

| 陈述 | 实际 |
|---|---|
| `README.md:65` 「注册 5 个模块」 | `main.go:46-51` 注册 **6** 个（含 `updater`） |
| `README.md:125` 自动更新「代码中完全没有」 | `modules/updater` 已是完整模块 |
| `README.md:96` Wails「尚未在 `internal/app` 接线」 | 已接线（`main.go:78 runNativeWindow`） |
| `README.md:167` `internal/app`、`modules/preferences` 尚无测试 | 两者均已有测试 |
| 测试用例数 | **实测 169**；但 `README:54` 写 175、`TODO.md:156` 写 202、`AGENTS.md` 写 210 —— **四个数字互相矛盾** |
| `README:113`/`AGENTS.md` 图标在 `assets/icons/` | 无该目录，图标是 `index.html:198-225` 的内联 SVG |
| `AGENTS.md:24` `%APPDATA%\GoBox\gobox.log` | `paths.go:44` 实为 `<data>/logs/gobox.log` |
| `DEVELOPMENT.md:69-84` 配置文件名与「`go build` 会失败」 | 实际为 `GoBox/config.yaml`；构建阻塞已解除 |
| `TODO.md:81/171`、`PROJECT-AUDIT.md:280` 引用 `internal/server/web/index.html` | 已迁至 `internal/panel/index.html` |
| `TODO.md:159` 第 18 项标 `- [ ]` 但正文已 ✅ | 状态未勾 |

- **建议**：测试用例数改为 CI 输出注入（或标注「以 `go test -v` 实际为准」），
  杜绝第 5 个数字出现。
- **TODO #11（`GoBox` 代号）**：文档已统一为 PCMannager，代码仍留 `GoBox`
  （`paths.AppName`、窗口类名、日志文件名）。改名会**破坏用户既有配置路径**，
  需提供迁移逻辑——建议单独排期，不要顺手改。
  **迁移设计已交付**：见 `docs/NAMING-MIGRATION.md`（全量盘点表：9 类运行时身份
  + 40 余处注释、迁移方案、不迁移的后果、建议排期）。盘点中发现一处**双端耦合**：
  `modules/updater/feature.go` 的 PowerShell helper 硬编码 `GoBox\logs`，
  而读取端 `updateDoneLog()` 走 `paths.DataDir("")`——改 `paths.AppName` 时
  若漏改此处，写读路径分叉、更新完成记录永不被消费。
  **已完成（本次）**：helper 脚本的 `$logDir` 改由 `updateDoneLog()` 传入（不再硬编码），
  写读同源；`TestBuildUpdateScript` 断言目录来自入参且脚本不再出现 `$env:APPDATA`。
  改名前置条件已解除，可单独排期。

---

## 执行顺序建议（2026-10-30 复核）

首轮 A–F 的主要工程整改及产品补齐已落地。当前推进建议按外部依赖和风险排序：

1. **B5 / TODO #49：真实 Windows 实机验收**。使用
   [`RELEASE-CHECKLIST.md`](RELEASE-CHECKLIST.md) 记录系统版本和逐项结果，覆盖托盘启退、
   热键、任务栏嵌入、截图/剪贴板、Wails 回退和 updater；这是本机 Linux 无法替代的验收。
2. **发布流程治理（承接 TODO #1）**。`v0.1.12` 已端到端验证版本注入、四平台产物、
   Release 上传与 Wiki 同步；剩余问题是 `v0.1.10`/`v0.1.11` 孤儿 tag 以及
   `auto-release.yml` 每次 push 自动发版的节奏，需维护者先裁定再改 workflow。
3. **repair Walk 面板实机确认**。代码已通过 `LockOSThread` 固定 Walk 窗口与 tooltip
   消息循环线程；在 Windows 上确认 `TTM_ADDTOOL failed` 不再出现，若仍复现则根据新日志继续定位。
4. **平台支持决策**。根据 [`TODO.md`](../TODO.md) 中各模块的实际降级能力，明确继续补齐
   Linux/macOS，还是在 README/面板中明确 Windows 优先；不要仅凭可交叉编译宣称功能等价。
5. **阶段 E 架构收敛**。`panelapi` DTO 抽包、`App` 拆分等大规模重构继续延后到实机验收
   和用户可见缺口收敛之后。

本地 `main` 与 `origin/main` 已同步（`v0.1.12`），Dependabot PR 均已合并；
后续提交直接推送即可触发 CI。

### 明确不做（本轮）

- 不新增功能模块。当前模块中已有 3 个（`selfcontext` 非 Windows、
  `screenshot` 编辑器非 Windows、`repair` 非 Windows）存在「宣称有、实际无」的问题，
  加第 7 个只会放大信任损耗。
- 不动 `GoBox` → `PCMannager` 代码改名（破坏配置路径，需迁移方案）。
- 不重构 `App` god-struct（阶段 E）。#9 实现、#11 迁移设计稿、#12/#13 对账结论均已完成；
  #11 的代码改名执行待排期，仍建议在 Windows 实机验收（B5）之后再动——
  功能补齐期重构会放大回归面。

---

## 附：首次分析核验方式（历史记录，2026-10-03）

所有结论均经实际执行核验，非静态推测：

- `go build ./...` / `go vet ./...` / `gofmt -l .` → 全绿
- `go test -race -count=1 ./internal/... ./modules/...` → 全绿，**169** 个用例
- SSE 缺陷：`server.go:312` 与 `index.html:314` 对照
- emoji 闸门：对照实验 `grep -Pl '\x{2705}' TODO.md`（匹配）
  vs 脚本裸字节区间（不匹配），且 `bash scripts/check-emoji.sh` 实测 exit 0
- `TODO.md` 实际含 U+2705 / U+2192（Python 逐码点枚举确认）
- updater 版本：`feature.go:177` vs `internal/app/version.go` vs `core/module.go:149`
- XSS：`index.html:455` 未走 `esc()`，全站唯一
- repair：`exec_windows.go:13` 无 `CommandContext`；`panel_windows.go:151` 绕过 `RunAction`
- CI：`ci.yml` 中 `go test`/`go vet` 无 `GOOS` 覆盖