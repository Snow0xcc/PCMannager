# PCMannager 项目审计与整改报告

- 报告日期：2026-09-23
- 审计基线：`HEAD 09bf174` 及当前未提交工作区
- 审计范围：架构、模块实现、配置、HTTP/SSE 面板、Win32 生命周期、测试、CI、文档和跨平台构建
- 审计方式：静态代码审查、全量测试、竞态测试、覆盖率统计、四平台交叉构建、Linux 运行时 smoke test
- 执行 Prompt：[`AI-PHASE-A-PROMPT.md`](AI-PHASE-A-PROMPT.md)
- 变更说明：本报告仅新增文档，没有修改业务代码

## 1. 执行摘要

项目已经具备较清晰的模块化骨架：

- `main.go` 负责组合根和模块注册。
- `internal/app` 负责配置、日志、事件总线、热键、托盘和模块生命周期。
- `internal/core.Module` 提供统一模块契约。
- `internal/server` 提供本地 REST、SSE 和内嵌 Web 面板。
- `modules/repair/catalog.go` 使用声明式目录统一维护工具条目。

当前代码可以编译、测试并生成四个目标平台的产物，但还不适合直接发布。主要阻塞不是功能数量，而是 Windows 运行时正确性和本地控制面安全性：

1. Windows 托盘 `Show()` 存在同锁重入死锁。
2. Windows 全局热键的注册线程和消息泵线程不一致。
3. Event Bus 存在向已关闭 channel 发送的竞态。
4. 回环 HTTP 服务没有认证和 CSRF 边界，repair 危险动作也没有服务端确认。
5. 配置引导会在当前工作目录生成一份随后未采用的配置。
6. SSE 服务端使用命名事件，前端只监听默认 `message`，实时事件链路未闭合。
7. taskbar 和应用主消息循环的 Win32 线程归属缺少可靠约束。

综合判断：项目处于 pre-alpha 阶段。下一阶段应优先完成稳定性、安全性和真实 Windows 验收，而不是继续横向增加功能。

## 2. 审计基线与工作区状态

报告生成时工作区不是干净状态，存在：

- 已修改的 `AGENTS.md`、`TODO.md`、`main.go`、`internal/app/app.go`、`internal/core/module.go` 和多个模块文件。
- 尚未跟踪的 `internal/server/`、测试文件、`modules/repair/catalog.go`、`docs/HANDOVER.md` 和 `scripts/`。

因此，本报告描述的是当前工作区实际状态，而不是只针对 `HEAD 09bf174`。后续修复和提交前必须重新检查差异，避免覆盖并行开发内容。

## 3. 架构现状

### 3.1 依赖关系

```text
main
  └─ internal/app
       ├─ internal/config
       ├─ internal/core
       ├─ internal/logx
       ├─ internal/paths
       ├─ internal/server
       ├─ internal/sysutil
       ├─ internal/tray
       └─ internal/winui

modules/*
  └─ internal/core.Context + core.Module
```

HTTP 层通过 `server.Provider` 接口隔离应用实现，具体适配器位于 `internal/app/provider.go`。当前依赖方向为 `app -> server`，没有形成反向循环。

### 3.2 主要数据流

配置更新：

```text
Web PATCH
  -> server handler
  -> panelProvider
  -> App.SetModuleSettings
  -> config.Manager
  -> Module.ApplyOption
  -> 可选模块重启
```

事件流：

```text
slog / core.Bus
  -> /api/events SSE
  -> 内嵌 Web 面板
```

托盘流：

```text
Shell_NotifyIconW
  -> tray.Handler
  -> App.onTraySelect
  -> 打开面板、启停模块、打开 UI、自启或退出
```

### 3.3 架构优点

- 组合根和业务模块分离，模块不直接依赖应用具体实现。
- `Option`、`Action` 和 `State` 为前端提供了声明式数据。
- repair 目录、Walk 面板和 Web 动作均可从同一 `Catalog()` 生成。
- `internal/winui` 集中封装了无 cgo Win32 调用。
- 平台相关文件普遍使用 `_windows.go` 与 `_other.go` 成对实现。
- `selfcontext` 默认关闭，符合隐私 opt-in 原则。

### 3.4 架构债务

- `core.Registry` 没有进入生产主链路，`App` 又维护了一套模块表、顺序和运行状态。
- `modules/preferences` 是未接入的旧 Walk 面板，并反向依赖 `internal/app`。
- 应用级设置中，主题、语言、WebView 等配置多数没有运行时消费者。
- 声明式字段 `VisibleIf`、`Admin`、参数 `Required` 尚未形成统一后端执行语义。

## 4. 功能模块评估

| 模块 | 当前能力 | 主要限制 |
| --- | --- | --- |
| `taskbar` | CPU、内存、磁盘、网络、运行时长采集；Windows 任务栏窗口 | Win32 线程归属不安全；pump 重启泄漏；多个布局选项未兑现 |
| `clipboard` | 文本和图片历史、回写、固定、清空、保留期裁剪 | 历史仅在内存；非 Windows 无完整查看器；图片缺少字节配额 |
| `screenshot` | 主显示器截图、Windows 区域编辑、保存和复制 | `copy_after` 未实现；非 Windows 编辑结果被丢弃；macOS 无 cgo 时截图库不支持 |
| `selfcontext` | 活动窗口标题和进程采样、摘要、导出 | 数据仅在内存；非 Windows 不采集；隐私数据会进入 Bus 历史 |
| `repair` | 68 个声明式工具条目，统一动作分派 | `Admin` 不提权；无超时和输出上限；API 可绕过前端确认 |
| preferences | 实际使用 HTTP Web 面板 | 旧 Walk 实现未接入，形成重复代码和依赖 |

repair 当前的目录 ID、Actions 暴露和 Lookup 分派链路基本一致。整改重点应放在执行器、权限和确认机制，而不是重写目录。

## 5. 验证结果

### 5.1 已通过项目

| 检查 | 结果 |
| --- | --- |
| `go test -count=1 ./...` | 通过 |
| `go test -race -count=1 ./internal/... ./modules/...` | 通过 |
| `go vet ./...` | 通过 |
| Windows 交叉 `go vet ./...` | 通过 |
| macOS 交叉 `go vet ./...` | 通过 |
| `go build ./...` | 通过 |
| `windows/amd64`、`CGO_ENABLED=0` | 通过 |
| `linux/amd64`、`CGO_ENABLED=0` | 通过 |
| `darwin/amd64`、`CGO_ENABLED=0` | 通过 |
| `darwin/arm64`、`CGO_ENABLED=0` | 通过 |
| `gofmt`，排除 `reference/` | 通过 |
| `git diff --check` | 通过 |

### 5.2 测试规模与覆盖率

当前共有 93 个顶层测试函数。

| 包 | 语句覆盖率 | 评价 |
| --- | ---: | --- |
| `internal/config` | 86.1% | 基础持久化覆盖较好 |
| `internal/server` | 83.1% | REST 和 SSE 冒烟覆盖较好 |
| `internal/core` | 72.2% | Registry、Bus、热键解析覆盖较好 |
| `modules/taskbar` | 41.6% | 数据采集和格式化有覆盖 |
| `modules/clipboard` | 34.6% | 主要覆盖 History，平台逻辑不足 |
| `modules/screenshot` | 27.7% | 覆盖配置和保存，编辑器不足 |
| `modules/repair` | 25.4% | 主要覆盖 catalog，执行层不足 |
| `internal/app` | 0% | 生命周期和一致性没有测试 |
| `internal/tray` | 0% | Windows 死锁未被测试发现 |
| `internal/winui` | 0% | 原生窗口线程行为没有测试 |
| `modules/selfcontext` | 0% | 采样、裁剪和隐私行为没有测试 |

### 5.3 Linux smoke test

使用隔离的 `XDG_CONFIG_HOME` 和 `PCMANNAGER_CONFIG` 启动 Linux 构建：

- HTTP 面板成功监听 `127.0.0.1` 随机端口。
- SIGTERM 可以触发应用退出流程。
- 无 X11 环境下剪贴板初始化失败并正确返回错误。
- 同一启动过程生成了当前配置目录和用户配置目录两份 `config.yaml`，确认配置引导存在重复写入。

### 5.4 未通过或需收口的检查

`go mod tidy -diff` 建议删除未使用的直接依赖：

```text
github.com/gonutz/w32 v1.0.0
```

`bash scripts/check-emoji.sh` 当前报告通过，但文件中实际存在 `U+2699`、`U+2702`、`U+2191` 和 `U+2193`。脚本的 Bash Unicode 展开方式存在漏报，同时 `git ls-files` 会忽略尚未跟踪的前端文件，因此该检查当前不能作为可靠门禁。

## 6. 风险清单

### 6.1 P0：发布阻断

#### P0-1 Windows 托盘同锁重入

位置：

- `internal/tray/tray_windows.go`：`Show()`、`Hide()`、`shellNotify()`

问题：

1. `Show()` 获取 `t.mu` 并保持到函数返回。
2. `Show()` 在持锁状态调用 `shellNotify()`。
3. `shellNotify()` 再次获取同一把 `t.mu`。

结果：Windows 启动流程会阻塞在 `StartTray()`，无法进入正常消息循环。

状态：**已修复（2026-09-23，阶段 A）** — 代码修复 + 自动化测试，但**尚未在真实 Windows 环境运行验证**。

修复内容：

- 删除 `shellNotify()`（它自身获取 `t.mu`），改为 `notify(action, nid)`：**不获取任何锁**，只接受调用者已在 `mu` 下复制好的 `NOTIFYICONDATAW` 快照。
- `Show()` / `Hide()` / `updateTooltip()` 统一遵循"锁内复制快照 → 解锁 → 调用 Win32"协议，彻底消除同 goroutine 重入。
- `SetMenu()` 改为读取锁内的 `added` 标志判断是否需要刷新 tooltip（原实现在锁外读 `t.nid.HWnd`，存在数据竞争）。
- `reAdd()` 在锁外调用 `Show()`，避免嵌套。
- `Hide()` 先置 `added=false` 再通知 shell，避免并发 `Show()` 观察到过期状态。
- 新增 `notifyFn` 注入点，使锁协议可在无真实 shell 的环境下被测试。

验收对照：

- Show、Hide、Destroy 均不会死锁 — 已由 `internal/tray/tray_windows_test.go` 覆盖（含 `TestTrayNoReentrantLock`：在已持锁状态下调用 `notify` 不得阻塞）。
- Explorer 重启后图标可恢复 — `TestTrayReAddAfterExplorerRestart` 覆盖（代码级）。
- **连续启动和退出 20 次无阻塞或残留窗口 — 未验证，需要真实 Windows 环境。**
- 并发 `SetMenu` 与 `Show`/`Hide` 在 race 模式下无数据竞争 — `TestTraySetMenuConcurrentWithShowHide` 覆盖（Windows 交叉编译通过；Windows 测试二进制经 `go test -c` 验证可编译，未在本 Linux 环境执行）。

#### P0-2 全局热键注册线程错误

位置：

- `internal/core/hotkey_windows.go`
- `internal/core/hotkey.go`

问题：`RegisterHotKey(NULL, ...)` 在调用 `Bind()` 的线程注册，`WM_HOTKEY` 却在专用线程读取。启动和 HTTP 重绑均不保证注册线程与消息泵线程相同。

整改方向：在热键线程创建 message-only window，通过命令 channel 串行执行注册、注销和停止，并等待 ready/join。

状态：**已修复（2026-09-23，阶段 A）** — 代码修复 + fake backend 测试，但**尚未在真实 Windows 环境验证热键实际触发**。

修复内容（采用"命令 channel + 专用泵线程执行"方案）：

- `winHotkeyBackend` 新增 `cmdCh`（命令队列）、`readyCh`（就绪同步）、`doneCh`（退出同步）。
- `register()` / `unregister()` 不再直接调用 Win32，而是把命令投递给泵线程并**同步等待结果**（`resp chan error`，容量 1，超时或泵已停止时也不会无人接收）。
- 泵线程 `run()` 仍 `runtime.LockOSThread()`，在 `GetMessage` 前后 `drain()` 命令队列，`doRegister`/`doUnregister` **只在该线程执行**，因此 `RegisterHotKey` 与 `WM_HOTKEY` 归属同一线程队列。
- `ready`：泵线程发布自身 TID 后才 `close(readyCh)`，保证 `Bind` 返回前泵已可接收命令，且唤醒消息能投递到正确线程。
- `join`：`close()` 置 `closed`、投递 `WM_QUIT`，并等待 `doneCh`（含 `joinTimeout` 兜底，绝不无限阻塞关停）；重复 `close` 安全。
- 投递命令后用 `PostThreadMessage(wmWake)` 唤醒阻塞中的 `GetMessage`，避免命令饿死。
- `HotkeyManager.Bind/Unbind/Stop` 改为**先锁内决策、再锁外调用后端**：后端调用现在会阻塞等待泵线程，持锁调用会把所有 `Bind/Combo/Conflicts` 调用方一起拖住。`Bind` 失败会回滚已占用的槽位，冲突检测与告警语义不变。
- 新增 `notifyFn` 之外的可注入边界：测试用 `fakeBackend` 模拟"阻塞式线程后端"。

验收对照：

- 注册、注销和 `GetMessage` 位于同一 OS 线程 — 由代码结构保证（`doRegister`/`doUnregister` 仅在 `run` 线程内被调用）；**未在真实 Windows 上验证热键触发**。
- Start/Bind 顺序不会把 `WM_HOTKEY` 投递到错误线程队列 — 由 `readyCh` + TID 发布顺序保证。
- Stop 可等待线程退出且无泄漏 — `TestHotkeyStopUnregistersAllThenCloses`（先注销全部再 close，且 close 恰好一次）覆盖。
- 非 Windows 的 unsupported 行为保持不变 — `hotkey_other.go` 未改动，`go test ./...` 通过。
- Windows 交叉测试编译和 `go vet` 通过 — `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build/vet` 均 0；`go test -c` 验证 Windows 测试二进制可编译（未在 Linux 上执行）。
- 未破坏重复 ID 拒绝、冲突报告、手动调用 — `TestHotkeyConflictsStillReported`、`TestHotkeyBindFailureRollsBack`、`TestHotkeyManualHandlerSurvivesRegistrationFailure` 覆盖。

#### P0-3 Bus 发送和关闭竞态

位置：`internal/core/bus.go`

问题：Publish 在锁外向订阅 channel 发送，而 unsubscribe 和 Close 可以并发关闭 channel，可能触发进程级 panic。

整改方向：在同一同步协议内完成非阻塞发送和关闭，或为订阅者增加独立 done channel 与串行关闭状态。

状态：**已修复（2026-09-23，阶段 A）** — 代码修复 + 确定性压力测试，`go test -race -count=5` 稳定通过。

修复内容：

- `subscriber` 去掉 `sync.Once`，改为 `mu + done` 二元组：所有发送走 `send()`，所有关闭走 `close()`，两者被同一把锁串行化，并以 `done` 为闸门。**任何路径都不可能向已关闭 channel 发送**（`send` 见 `done` 即返回 false）。
- 明确锁顺序恒为 `Bus.mu → subscriber.mu`，绝不反向，消除死锁可能。
- **删除异步历史回放 goroutine**（它是"在 Subscribe 返回后仍向 channel 发送"的竞态源），改为在 `Bus.mu` 下同步预填历史。因为缓冲容量 `subBuffer=256` 大于 `maxHist=200`，预填永不阻塞，因此既保持 `Subscribe` 非阻塞，又保证重放事件严格早于实时事件（顺序性反而更强）。
- `unsubscribe` 幂等（`done` 闸门）、重复 `Close` 安全、慢订阅者仍丢事件不阻塞 `Publish`。

验收对照（`internal/core/bus_race_test.go`，全部 `-race` 下重复 5 次通过）：

- 多 publisher 并发 Close — `TestBusConcurrentCloseWithPublishers`（barrier 同步 + 订阅者churn）。
- 多订阅者并发 Subscribe/Publish/unsubscribe/Close — `TestBusConcurrentSubscribePublishUnsubscribeClose`。
- 历史回放期间立刻 unsubscribe — `TestBusUnsubscribeImmediatelyAfterSubscribe`（含重复 unsubscribe）。
- 重复 Close — `TestBusRepeatedCloseIsSafe`。
- 慢订阅者不阻塞 Publish — `TestBusSlowSubscriberNeverBlocksPublish`（5000 次发布有超时断言）。
- SSE handler 可退出 — `TestBusSubscribeAfterCloseReturnsClosedChannel`（Close 后 Subscribe 返回已关闭 channel）。
- 测试不使用 sleep 碰运气，全部采用 barrier / atomic / 超时断言。

#### P0-4 本地 API 缺少安全边界

位置：

- `internal/server/server.go`
- `internal/server/web/index.html`
- `modules/repair/feature.go`

问题：回环监听和随机端口只能降低暴露面，不能替代认证。当前没有 token、Origin、Host、CSRF、CSP 或请求体限制。危险确认只在前端，Admin 标记不执行提权。

整改方向：

1. 启动时生成高熵会话凭据。
2. 所有 API 和 SSE 默认校验凭据。
3. 严格校验 Host 和 Origin。
4. 写操作增加 CSRF 防护。
5. 危险动作使用服务端一次性确认凭据。
6. 动作执行前检查模块状态、平台能力和并发锁。

### 6.2 P1：高优先级稳定性问题

#### P1-1 配置路径解析错误

位置：

- `main.go`：`configDirEnv()`
- `internal/app/app.go`：`New()`、`defaultDataDir()`
- `internal/config/config.go`：`Load()`

问题：

- 默认先在当前工作目录创建 `config.yaml`。
- 随后又加载用户配置目录中的另一份配置。
- `PCMANNAGER_CONFIG` 文档称支持配置文件路径，实际只切换目录并固定读取 `config.yaml`。
- 从只读安装目录启动可能提前失败。

整改方向：建立唯一配置路径解析器，环境变量明确区分文件和目录，不再默认探测当前工作目录。

#### P1-2 配置更新缺少事务和统一验证

问题：

- 先写配置，再调用模块校验。
- 模块校验错误只写日志，API 仍可返回成功。
- 多次 option 更新逐项保存，后项失败时前项已经落盘。
- 并发保存共用固定 `.tmp` 文件。

整改方向：先统一验证全部字段，再一次性提交内存和磁盘；运行时应用失败时回滚；Manager 增加专用写锁。

#### P1-3 SSE 前端链路未闭合

服务端发送：

```text
event: log
event: state
event: progress
event: notice
```

前端只使用 `es.onmessage`，因此不会收到上述命名事件。五秒轮询只更新内存状态，也没有重新渲染当前页面。

整改方向：为每种事件注册 listener，收到 state 后更新模块并重绘；增加真实浏览器级 EventSource 测试。

#### P1-4 Win32 线程和窗口销毁不安全

- 主应用创建托盘窗口和运行消息循环的 goroutine没有固定 OS 线程。
- taskbar widget 注释声称锁定线程，但没有调用 `runtime.LockOSThread`。
- `Window.Destroy()` 可从非窗口所属线程直接调用。
- 信号 goroutine 调用 `PostQuitMessage` 时，消息不一定进入主消息泵线程队列。

整改方向：所有 HWND 的创建、消息循环和销毁都归属同一 OS 线程；跨线程只发送 `PostMessage`。

#### P1-5 taskbar 后台生命周期泄漏

Collector 的 `Stop()` 只关闭 done channel，不关闭输出 channel。旧 pump 在模块重启后会继续等待，直到应用退出。

整改方向：由 Collector 拥有输出 channel 的关闭责任，pump 使用 WaitGroup 等待退出。

### 6.3 P2：功能一致性债务

- screenshot 的 `copy_after` 只声明和校验，没有实际行为。
- taskbar 的 layout、num_align、follow_theme、multi_monitor 等选项未完整兑现。
- repair 的 Admin 没有调用 UAC 提权。
- repair 使用 `CombinedOutput()`，没有 context、超时、取消、输出上限或动作互斥。
- `confirm_danger` 只对旧 Walk 路径有效。
- `VisibleIf` 和参数 Required 没有统一前端或后端执行。
- 禁用模块仍可能通过 API 直接调用 UI、热键逻辑或动作。
- 单实例实现存在但没有接入启动流程。
- clipboard、selfcontext 历史仅存在内存，retention 不跨重启生效。
- 剪贴板摘要和活动窗口标题会进入 Bus 历史，需要明确隐私策略。

## 7. 跨平台支持结论

当前应将支持等级描述为：

| 平台 | 当前等级 | 说明 |
| --- | --- | --- |
| Windows | 主目标，未完成运行时验收 | 原生 UI 最完整，但存在托盘、热键和线程阻塞问题 |
| Linux | 编译兼容和部分后台能力 | 无托盘、无全局热键、部分 UI 不可用、剪贴板依赖显示环境 |
| macOS | 编译兼容 | 无 cgo 截图不可用，无 launchd，自启实现错误复用 Linux desktop |

在完成对应实现前，不应将 Linux/macOS 描述为与 Windows 功能等价。

## 8. 文档、依赖和交付问题

### 8.1 文档漂移

需要统一修订：

- README 仍称 Web/Wails 尚未落地，实际已有 HTTP Web 面板。
- README 仍使用旧模块 ID、旧热键、旧配置文件名和旧日志名。
- DEVELOPMENT 仍称项目无法构建。
- HANDOVER 将架构描述为已经稳定，未记录运行时 P0。
- TODO 将 P0 理解为仅构建阻塞已经解除，容易掩盖运行时阻塞。
- AGENTS 中仍存在 `core.Registry` 已用于生产、旧包名和 `FeatureConfig` 等过时描述。
- 内部 `GoBox` 与产品名 `PCMannager` 仍并存。

### 8.2 CI 和发布

当前只有 tag release workflow，缺少：

- PR/push CI。
- go test、race 和 go vet 门禁。
- Windows runner 测试。
- 浏览器级前端测试。
- emoji 门禁。
- `go mod tidy -diff` 门禁。
- 版本注入和 `--version`。
- checksum、SBOM 和发布验证。
- 根目录 LICENSE。

Release workflow 还会递归拉取 `reference/` 子模块，但主构建不依赖这些目录，会增加不必要的网络和供应链故障面。

## 9. 实施路线图

### 阶段 A：解除运行时阻断

- [ ] 修复 tray mutex 重入。
- [ ] 重构 Windows 热键线程命令模型。
- [ ] 修复 Bus Publish/unsubscribe/Close 协议。
- [ ] 为上述问题增加回归测试和 Windows smoke test。
- [ ] 固定主消息循环线程并定向退出消息。

### 阶段 B：加固配置和控制面

- [ ] 重写 `PCMANNAGER_CONFIG` 和默认配置路径解析。
- [ ] 增加统一 option 验证和事务更新。
- [ ] 为 HTTP/SSE 增加认证、Origin、Host 和 CSRF 防护。
- [ ] 为危险 repair 动作增加服务端确认。
- [ ] 限制请求体、动作输出和并发执行。

### 阶段 C：修复前端和模块生命周期

- [ ] 修复命名 SSE 事件监听。
- [ ] 实现 `VisibleIf` 和需要重启设置的反馈。
- [ ] 修复 taskbar pump 和 Win32 窗口线程模型。
- [ ] 让 Stop 关闭原生 UI 并等待后台任务退出。
- [ ] 决定实现或删除 `copy_after`、未接线 taskbar 选项和无效应用设置。

### 阶段 D：建立发布门禁

- [ ] 增加 PR CI。
- [ ] 执行测试、race、vet、四平台构建和前端检查。
- [ ] 执行 `go mod tidy -diff` 并删除未使用依赖。
- [ ] 注入版本、提交号和构建时间。
- [ ] 增加 LICENSE、第三方许可证清单、checksum 和 SBOM。
- [ ] 修订 README、DEVELOPMENT、HANDOVER、TODO 和 AGENTS。

## 10. 首个可发布版本验收门槛

在发布 RC 前至少满足：

- Windows 连续启动和退出 20 次无死锁、panic 或残留进程。
- Explorer 重启后托盘可以恢复。
- 热键注册、注销、冲突和退出均通过真实 Windows 测试。
- Bus 并发关闭压测在 race 模式下通过。
- 未认证、跨 Origin、非法 Host 和超大 body 请求被拒绝。
- 危险动作没有服务端确认时被拒绝。
- SSE 在真实浏览器中可以显示 log、state、progress 和 notice。
- 配置清空热键、并发更新、损坏恢复和失败回滚均通过测试。
- repair 命令具备超时、取消、输出上限和并发互斥。
- 四个目标平台构建通过，PR CI 全绿。
- README 不再描述未实现能力，版本信息与发布产物一致。

## 11. 复验命令

```bash
go test -count=1 ./...
go test -race -count=1 ./internal/... ./modules/...
go test -count=1 -cover ./...
go vet ./...
go build ./...

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
gofmt -l . | grep -v '^reference/'
git diff --check
go mod tidy -diff
```

## 12. 最终结论

项目值得继续沿当前模块化方向演进，不建议推倒重写。现有模块契约、声明式 repair 目录、本地 HTTP 面板和无 cgo WinUI 底层均可保留。

在开始新增功能前，应先完成阶段 A 和阶段 B。真实 Windows 运行时稳定性、Bus 并发正确性和本地面板授权边界，是项目从可编译原型进入可发布版本的三项前置条件。
