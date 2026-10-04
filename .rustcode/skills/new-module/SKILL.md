---
name: new-module
description: 按项目约定新建一个 PCMannager 功能模块（modules/<name>/，实现 core.Module）。当用户要求添加新模块或新功能包时使用。
disable-model-invocation: true
---

为 `$ARGUMENTS` 新建功能模块。全程以 `AGENTS.md` 的"关键开发约定"为准，依次完成：

1. 在 `modules/<name>/` 建包，实现 `internal/core.Module` 接口；构造函数命名 `NewFeature()`。**包名必须等于目录名**（早年 `statusbar`/`pcrepair` 的旧名已改齐，见 TODO #12），不要引入新的目录名/包名不一致。
2. 模块 id 必须稳定（Registry 与配置/热键按 id 查找），并同步 `internal/config` 的模块列表结构。
3. 用 `internal/core/registry` 的 `Register` 注册，并在 `main.go` 组装。
4. Windows 专用实现放 `*_windows.go`（`//go:build windows`），非 Windows 必须提供同名 `*_other.go` no-op；非 Windows 端不得 import `walk`/`w32`。
5. 需要"改配置即重启"的选项用 `core.Option` 的 `Restart: true` 声明，禁止 Help 前缀 hack。
6. 模块失败原因通过 `ModuleInfo.LastError`（`lastError` 槽位 + `errorAt` 时间戳）上报面板。
7. 补测试并跑 `go test -race -count=1 ./modules/...`，避开 AGENTS.md 记录的三个已知测试坑。
8. 跑 `go mod tidy`、`gofmt -w .`、`go vet ./...`；界面文案不得出现 emoji（`bash scripts/check-emoji.sh`）。
9. 同一次改动中更新 `AGENTS.md`（顶层结构、约定、模块清单）。
