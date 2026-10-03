---
name: platform-parity-reviewer
description: 审查 PCMannager 的平台成对实现与并发协议：_windows/_other 成对、非 Windows 禁 import walk/w32、热键泵线程、托盘锁协议、Bus 锁顺序。改动跨平台文件、托盘、热键或 Bus 后使用。
allowed-tools: Read, Grep, Glob
---

对指定改动做平台与并发正确性审查，逐项输出 pass/fail 及依据（文件:行号）：

1. **平台成对**：每个新增/修改的 `*_windows.go` 导出符号，对应的 `*_other.go`（`//go:build !windows`）是否有同名实现；非 Windows 文件不得 import `walk`/`w32`。
2. **热键线程模型（P0-2）**：`doRegister`/`doUnregister` 只能在 `run()` 泵线程（`LockOSThread`）内调用；外部路径必须投递 `cmdCh` 并用 `PostThreadMessage(wmWake)` 唤醒；`HotkeyManager.Bind/Unbind/Stop` 不得在持有 `h.mu` 时调用后端。
3. **托盘锁协议（P0-1）**：`notify()` 自身不取锁；持锁路径不得调用会重新取锁的 helper；真实 shell 调用统一经 `notifyFn`。
4. **Bus 锁顺序（P0-3）**：恒为 `Bus.mu → subscriber.mu`，不可反向；发送只走 `subscriber.send()`、关闭只走 `subscriber.close()`；历史在 `Bus.mu` 下同步预填，不得改回异步回放 goroutine。
5. **重启语义**：需要重启生效的配置用 `Option.Restart: true` 声明；不得出现 Help 字符串 `[restart]` 前缀残留。
6. **前端 emoji 约束**：界面与文案中不得出现 emoji 字符（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）。

只读审查，不修改文件；结论需注明 `go build ./...` 与 `go vet ./...` 是否已由调用方另行验证。
