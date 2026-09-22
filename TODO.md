# TODO — 交接与推进清单

按**优先级 P0→P3**排列。接手建议从 P1 起（P0 已解除）。

## P0 — 构建阻塞 ✅ 已解除

~~`undefined: core.App` 导致四平台全部构建失败~~ → **已修复**（全模块迁移到 `core.Module` 契约 + `main.go` 改用 `internal/app` 装配层）。

实测结果（`CGO_ENABLED=0`）：

| 目标 | 构建 | vet | gofmt |
| --- | --- | --- | --- |
| `windows/amd64` | 通过 | 通过 | 通过 |
| `linux/amd64` | 通过 | 通过 | 通过 |
| `darwin/amd64` | 通过 | 通过 | 通过 |
| `darwin/arm64` | 通过 | 通过 | 通过 |

复验命令：

```bash
for t in "windows amd64" "linux amd64" "darwin amd64" "darwin arm64"; do
  set -- $t; out="pcmannager-$1-$2"; [ "$1" = windows ] && out="$out.exe";
  GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out" . || echo "FAIL $1/$2"
done
gofmt -l . | grep -v reference/   # 应无输出
go vet ./...
```

## P1 — 待验证与收尾

- [ ] **1. 实际跑一次 CI 发布**：推送测试 tag（如 `v0.0.1-rc1`）验证 `.github/workflows/release.yml` 全流程，
      确认 4 个产物 `pcmannager-<os>-<arch>[.exe]` 均生成并上传 Release。本地已能构建，但 CI 环境未实测。
- [ ] **2. 提交当前未入库的改动**：`git status` 中 `main.go`、`modules/*`（多个模块重写/删除文件）、
      `internal/core/module.go`、`internal/winui/api_windows.go`、`.github/workflows/release.yml` 尚未提交；
      另有未跟踪的 `docs/MODULE-CONTRACT.md`、`internal/server/`、`.rustcode/`。
      **注意**：`.rustcode/` 疑似编辑器/工具目录，提交前确认是否应加入 `.gitignore`。
- [ ] **3. `go mod tidy`**：迁移后依赖可能变化（旧 walk 依赖是否仍需保留），跑一遍并确认 `go.mod`/`go.sum` 干净。
- [ ] **4. 确认 `internal/server/` 的作用**：新增目录（含 `provider.go`），疑似面板服务端，需确认是否应接入 `app`（当前未被引用）。

## P2 — 功能补齐（规划中，见 README）

- [ ] **5. 托盘接线**：`internal/tray` 已实现（`Shell_NotifyIcon` + 消息窗口自愈、Explorer 重启自动重注册），
      但**仍无调用方**——`internal/app.App` 未持有 tray 字段。需在装配层 `tray.New(log, handler)` 并生成菜单
      （模块开关/热键/打开面板/退出），非 Windows 端由面板承接。
- [ ] **6. 开机自启**：`config.App.Autostart` 可持久化，但**无人写注册表**。需用
      `internal/winui/registry_windows.go` 已绑定的 API 实现
      `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的写入/删除；macOS/Linux 另需 launchd/systemd/.desktop 方案。
- [ ] **7. 自动更新**：代码中**完全没有**。规划用 GitHub Releases API 比对版本 + `go-github-selfupdate` 类库，
      托盘菜单加"检查更新"入口。
- [ ] **8. 首选项面板迁移到 Wails**：当前仍是 `lxn/walk` 原生对话框（仅 Windows）。
      规划改为 Wails Web 前端（`frontend/`），复用 `internal/config.Manager/ModuleView` 与 `core.Bus` 的 SSE 通道。
      - [ ] 建 `frontend/` 后**必须**加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）——
            客户端界面禁用 emoji，一律用 icon 资源替代。
- [ ] **9. 托盘图标资源**：`defaultIcon()` 目前硬编码回退 shell 通用图标，无自定义图标文件、无配置项。
      需补图标资源与（可选）配置化路径，并与前端 icon 规范一致。

## P3 — 卫生与规范

- [ ] **10. 补测试**：当前**无任何测试文件**。`modules/*` 与 `internal/core`（`Bus`、`Registry`、`HotkeyManager`、
      `config.Manager` 的默认值合并/原子保存）优先补单测。
- [ ] **11. 命名不一致**：代码内部代号 `GoBox`（`paths.AppName`、`tray` 窗口类名 `GoBoxTray`、日志 `gobox.log`、
      `internal/core` 注释）与仓库名 `PCMannager` 并存。文档已统一用 PCMannager，**代码待改**。
- [ ] **12. 模块目录名/包名核对**：迁移后应为 `taskbar`/`repair`（原 `statusbar`/`pcrepair`），
      已改但需确认无残留引用；`preferences` 非模块（注册表视图）。
- [ ] **13. `modules/*` 中仍存在的平台差异**：确认为 `_windows.go`/`_other.go` 成对且签名一致，
      非 Windows 端不得 import `walk`/`w32`/`systray`/`gohook`。

## 交接须知

- 克隆后必须 `git submodule update --init --recursive`（`reference/` 是 submodule，含 5 个第三方仓库，不参与主构建）。
- 本地调试：`PCMANNAGER_CONFIG=/path/to/config.yaml go run .`。
- 模块开发前**必读** [`docs/MODULE-CONTRACT.md`](docs/MODULE-CONTRACT.md)（接口、12 条硬性规则、验证命令）。
- 构建/调试命令见 [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)；架构与开发约定见 [`AGENTS.md`](AGENTS.md)。
- 历史演进见 [`CHANGELOG.md`](CHANGELOG.md)。
