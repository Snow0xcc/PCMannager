# PCMannager 阶段 A 一键执行 Prompt

本文件用于把 `docs/PROJECT-AUDIT.md` 中的阶段 A 直接交给编码 AI 执行。复制下面的完整代码块即可。

```text
你正在维护仓库 /workspaces/PCMannager。请作为主实现代理直接完成 PCMannager 的“阶段 A：解除运行时阻断”，不要只给建议，必须实际检查代码、修改代码、补测试并执行验证。

一、开始前必须完成

1. 阅读并遵守：
   - AGENTS.md
   - docs/PROJECT-AUDIT.md
   - docs/MODULE-CONTRACT.md
   - docs/DEVELOPMENT.md
2. 执行并检查：
   - git status --short
   - git diff
   - git diff --cached
3. 当前工作区存在大量用户未提交改动。必须保留这些改动，不得 reset、checkout、clean、覆盖或回退不属于本任务的内容。
4. 先重新验证 docs/PROJECT-AUDIT.md 中三个 P0 是否仍存在。若并行改动已经修复其中某项，验证证据后保留正确实现，不要重复改写。
5. 禁止修改 reference/ 子模块内容，禁止从 reference/ import 代码。
6. 禁止新增与本轮无关的功能、自动更新、Wails 迁移、插件系统或大规模重构。

二、本轮唯一目标

只处理以下三个 P0：

P0-1：修复 Windows 托盘同锁重入死锁
P0-2：修复 Windows 全局热键注册线程与消息泵线程不一致
P0-3：修复 Event Bus 向已关闭 channel 发送的竞态

目标是让项目达到“可进入真实 Windows 验收”的状态。本轮不要顺手实现 P1/P2 功能；可以修复直接服务于三个 P0 的基础问题，但必须说明关联原因。

三、P0-1 托盘死锁修复要求

重点文件：
- internal/tray/tray_windows.go
- internal/tray/tray.go
- internal/app/app.go
- internal/app/app_windows.go

当前风险：
- Show() 持有 t.mu 时调用 shellNotify()。
- shellNotify() 再次获取 t.mu，形成确定的同锁重入死锁。
- Hide() 也存在相同嵌套模式。

实现要求：

1. 消除所有同一 goroutine 对 t.mu 的重入。
2. 优先采用清晰的职责划分：
   - 需要在锁内复用的函数使用明确的 locked 版本；
   - 或在锁内复制必要快照，再在锁外执行 Shell_NotifyIconW；
   - 或在确认安全持锁后再进行一次原子状态转换。
3. 不得通过去掉所有锁、忽略并发或延迟关闭来规避问题。
4. 保持以下语义：
   - 重复 Show 幂等；
   - Hide 幂等；
   - SetMenu 可与 Show、Hide、Destroy 并发；
   - Explorer 重启后可重新添加图标；
   - Destroy 最终释放窗口；
   - 回调与状态字段不存在数据竞争。
5. 检查 WM_DESTROY、reAdd、updateTooltip 是否存在重入或生命周期问题。若属于本 P0 的直接调用链，一并修复。
6. Windows HWND 的创建、消息泵、销毁必须明确线程归属。评估 main goroutine 是否需要 runtime.LockOSThread，以及信号退出应如何定向通知主消息泵。若不改变线程模型无法安全修复，应在本轮完成最小必要修复并补测试。
7. 添加可自动化的回归测试。若 Win32 调用难以在 Linux CI 直接执行，应通过可注入的最小通知/窗口边界进行单元测试，同时增加 Windows build-tag 测试或编译检查。不要仅依靠注释说明。

P0-1 验收：

- Show、Hide、Destroy 不再嵌套获取同一 mutex。
- 在超时保护的测试中不会死锁。
- 并发 SetMenu 与 Show/Hide 在 race 模式下无数据竞争。
- Windows 目标编译通过。

四、P0-2 Windows 热键线程修复要求

重点文件：
- internal/core/hotkey.go
- internal/core/hotkey_windows.go
- internal/core/hotkey_other.go
- internal/app/app.go

当前风险：
- RegisterHotKey(NULL, ...) 在调用 Bind/Unbind 的线程执行。
- WM_HOTKEY 在另一个专用线程读取。
- 注册线程与消息泵线程不一致，热键事件不能稳定路由。

实现要求：

1. RegisterHotKey、UnregisterHotKey 和消息泵必须归属同一 OS 线程。
2. 不得继续从 HTTP handler、main goroutine 或任意临时 goroutine直接操作注册线程专属资源。
3. 优先采用以下方案之一：
   - 在热键线程创建 message-only HWND，并使用该 HWND 注册热键；
   - 或建立线程命令 channel，通过 PostThreadMessage 唤醒专用线程，在该线程执行注册和注销。
4. 必须提供 ready 同步，保证 Start 返回或 Bind 首次调用前，消息泵已经可接收命令。
5. 必须提供 join 或等价停止同步，保证 Stop 返回时：
   - 所有热键已注销；
   - WM_QUIT 已投递；
   - 专用线程已经退出；
   - 不会遗留 WaitGroup、goroutine 或线程资源。
6. 处理 Stop 早于 ready、重复 Stop、注册失败、注销失败和 channel 关闭等边界。
7. 不得破坏 HotkeyManager 的重复 ID 拒绝、冲突报告和手动热键调用。
8. 如注册命令需要同步等待结果，命令结构中必须包含 response channel 和 error，避免超时后无人接收。
9. 添加 fake backend 测试 Manager 行为；Windows 专用代码至少通过 windows/amd64 编译和可行的自动化测试。不要在非 Windows 测试中假装验证了 RegisterHotKey 的真实线程归属。

P0-2 验收：

- Windows backend 的注册、注销和 GetMessage 位于同一 OS 线程。
- Start/Bind 顺序不会把 WM_HOTKEY 投递到错误线程队列。
- Stop 可等待线程退出且无泄漏。
- 非 Windows 的 unsupported 行为保持不变。
- Windows 交叉测试编译和 go vet 通过。

五、P0-3 Event Bus 修复要求

重点文件：
- internal/core/bus.go
- internal/core/bus_test.go
- 使用 Bus 的 logx、server 和 app 关闭链路

当前风险：
- Publish 在锁内复制 subscriber，在锁外发送。
- unsubscribe 和 Close 会关闭 subscriber channel。
- 并发时可能触发 send on closed channel。
- 历史回放 goroutine 也可能向已关闭 channel 发送。

实现要求：

1. 明确定义以下操作之间的同步协议：
   - Publish；
   - Subscribe；
   - unsubscribe；
   - Close；
   - 历史回放。
2. 不允许任何执行路径向已关闭 channel 发送。
3. 慢订阅者仍不能阻塞模块线程；慢客户端事件丢失策略可以保留。
4. Close 仍应关闭订阅者或以其他方式可靠解除 SSE handler 的阻塞。
5. unsubscribe 应幂等。
6. 重复 Close 必须安全。
7. 历史回放和实时事件不得因为并发关闭而 panic。
8. 如当前异步历史回放无法保证顺序，可改为在锁内为 subscriber 预填历史后启动实时读取；不要为此引入会阻塞 Publish 的无限队列。
9. 增加确定性压力测试：
   - 多 publisher 并发 Close；
   - 多订阅者并发 Subscribe、Publish、unsubscribe、Close；
   - 历史回放期间立刻 unsubscribe；
   - 重复 Close；
   - 在 race 模式下重复执行。
10. 测试不得通过 sleep 碰运气来证明安全；应使用 barrier、hook 或足够可控的并发同步。

P0-3 验收：

- 并发关闭压力测试不 panic。
- `go test -race` 稳定通过。
- SSE handler 在订阅 channel 关闭或 request context 取消后都能退出。
- 慢订阅者不会阻塞 Publish。

六、代码与测试约束

1. 保持现有模块契约和 app -> server 依赖方向。
2. 不修改模块业务功能。
3. 不通过关闭 race detector、延长 sleep、吞掉 panic 或跳过测试来制造通过结果。
4. 新增错误要带模块、操作和底层原因。
5. 新增并发代码必须说明锁顺序。
6. 保持 Windows 与非 Windows build tag 成对。
7. 非 Windows 文件不得 import walk、w32、systray 或 gohook。
8. 用户可见 UI 和文档禁止 emoji。
9. 保持 Go 代码通过 gofmt。
10. 不得修改 go.mod，除非确实新增或删除依赖；若修改依赖，先运行 go mod tidy 并报告差异。
11. 当前 go mod tidy -diff 已知会建议删除未使用的 github.com/gonutz/w32，但该清理不应阻塞本轮 P0 修复；不要顺手进行无关依赖整理。

七、必须执行的验证

至少执行：

```bash
go test -count=1 ./...
go test -race -count=1 ./internal/... ./modules/...
go vet ./...
go build ./...

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go vet ./...

gofmt -l . | grep -v '^reference/'
git diff --check
```

如果新增 Windows build-tag 测试，额外使用 go test -c 或项目可用的等价方式确认 Windows 测试二进制能够编译。不要在 Linux 上尝试执行 Windows 测试二进制并伪造结果。

八、文档同步要求

完成代码后：

1. 更新 docs/PROJECT-AUDIT.md：
   - 标记已实际修复并有验证证据的 P0；
   - 保留尚未在真实 Windows 环境验证的限制；
   - 更新测试命令和结果摘要。
2. 更新 TODO.md 中与三个 P0 直接相关的状态，但不要把 P0 整体标为完成，除非每个验收项都有证据。
3. 如果架构、构建方式、线程模型或模块契约发生变化，同步更新 AGENTS.md。
4. docs/HANDOVER.md 如继续声称“架构已稳定”而未反映本轮状态，应做最小修订。
5. 不得把尚未执行的真实 Windows 测试写成“已通过”。

九、停止条件

只有以下情况可以停止实现并向用户报告阻塞：

1. 修复需要改变公开 HTTP API 或配置格式，且没有兼容方案。
2. 必须修改 reference/ 子模块。
3. 必须丢弃或覆盖用户现有未提交改动。
4. Windows 线程模型需要大规模替换 winui 架构，超出本轮安全范围。

遇到普通编译错误、测试失败或设计细节，应自行排查并继续，不要只描述问题。

十、最终回复格式

完成后按以下格式输出：

1. 已修复问题
   - P0-1：根因、修改、验证
   - P0-2：根因、修改、验证
   - P0-3：根因、修改、验证
2. 修改文件清单
3. 新增或调整的测试
4. 实际执行的命令与结果
5. 未验证事项，尤其是真实 Windows 运行验证
6. 仍存风险和下一阶段建议
7. 明确说明没有提交 Git commit，除非用户另行要求

完成标准：三个 P0 都有代码修复、针对性测试和可重复的验证证据；不能只修改文档或只解释问题。
```
