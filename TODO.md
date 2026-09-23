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

## P0 — 阶段 A：运行时阻断（见 docs/AI-PHASE-A-PROMPT.md）

三个发布阻断项均已完成**代码修复 + 自动化测试**，但**均未在真实 Windows 环境实机验证**：

- [x] **P0-1 托盘同锁重入死锁**（`internal/tray/tray_windows.go`）：删除自取锁的 `shellNotify()`，
      改为不持锁的 `notify(action, nid)`；`Show/Hide/updateTooltip` 统一"锁内快照、锁外调 Win32"；
      `SetMenu` 改读锁内 `added`（原锁外读 `t.nid.HWnd` 有数据竞争）；新增 `notifyFn` 注入点。
      测试 `internal/tray/tray_windows_test.go`（Windows build-tag，`go test -c` 验证可编译）。
      **未验证**：连续启退 20 次无残留窗口、真实 Explorer 重启恢复。
- [x] **P0-2 热键注册线程错误**（`internal/core/hotkey_windows.go`、`hotkey.go`）：改为命令 channel +
      泵线程执行；`doRegister/doUnregister` 只在 `LockOSThread` 泵线程内调用，外部同步等待结果；
      新增 `readyCh`（TID 发布后才就绪）与 `doneCh`（Stop 可 join，含超时兜底）；投递后
      `PostThreadMessage(wmWake)` 唤醒泵。`HotkeyManager.Bind/Unbind/Stop` 改为**锁内决策、锁外调后端**。
      测试 `internal/core/hotkey_manager_test.go`（fake backend 模拟阻塞式线程后端）。
      **未验证**：真实 Windows 上热键能否稳定触发。
- [x] **P0-3 Bus 发送/关闭竞态**（`internal/core/bus.go`）：`subscriber` 改为 `mu+done`，
      发送走 `send()`、关闭走 `close()`，锁顺序恒为 `Bus.mu → subscriber.mu`；
      **删除异步历史回放 goroutine**改为锁内同步预填（`subBuffer` 256 > `maxHist` 200，不阻塞）。
      测试 `internal/core/bus_race_test.go`（7 个确定性压测，barrier/atomic 而非 sleep），
      `go test -race -count=5` 稳定通过。
- [ ] **真实 Windows 验收**（承接阶段 A）：需在 Windows 机器上跑托盘启退循环、热键触发、
      Explorer 重启恢复、截图/剪贴板/任务栏嵌入实机验证。当前环境为 Linux，无法执行。

## 真实 Windows 实机反馈（2026-09-24，v0.1.0-rc1 之后）

用户提供了真实 Windows 运行日志，暴露出以下已修/待验项：

- [x] **19. 启动弹出黑色控制台窗口**：Go 默认构建 console 子系统。新增
      `scripts/build.sh` 统一构建参数（CI 与本地同源），对 Windows 追加 `-H windowsgui`；
      `release.yml` 的 build 步骤改为调用该脚本。已验证产物为 `PE32+ executable (GUI)`。
      **代价**：GUI 子系统下 stdout/stderr 不可见，排障需看日志文件或面板事件日志页。
- [x] **20. taskbar 小组件创建失败**（`CreateWindowEx 失败: Cannot create a top-level child window`）：
      `widget_windows.go` 用 `WS_CHILD` 却传 `parent=0`，Windows 必然拒绝。已改为以
      `winui.FindTaskbar()` 的 `Shell_TrayWnd` 为父窗口；找不到任务栏时降级为
      `WS_POPUP|WS_EX_TOPMOST` 悬浮窗（同时改 style，而非只换 parent），并报 `errNoTaskbar`。
      **未验证**：需在真实 Windows 上确认小组件能嵌入任务栏显示。
- [ ] **21. 热键被占用**（`RegisterHotKey 失败: Hot key is already registered`）：`F1` 与
      `Ctrl+\`` 在用户机器上已被其它程序占用，属**真实环境冲突而非代码缺陷**——当前已按设计降级
      （告警 + 面板/托盘仍可用）。待办：在面板热键编辑器里加"可用性检测"提示，并允许用户改绑。

## P1 — 待验证与收尾

- [ ] **1. 实际跑一次 CI 发布**：推送测试 tag（如 `v0.0.1-rc1`）验证 `.github/workflows/release.yml` 全流程，
      确认 4 个产物 `pcmannager-<os>-<arch>[.exe]` 均生成并上传 Release。本地已能构建，但 CI 环境未实测。
- [x] **2. 提交当前未入库的改动** ✅ 已完成（`ac6cae4`）：`internal/wailsapp/`、`internal/panel/`
      （含 `panel.go`）、`scripts/build.sh`、`main_{windows,other}.go` 等全部入库。
      `.rustcode/` 与根目录 `config.yaml`（app 落盘的用户配置副本，正常位置是数据目录下）
      已加入 `.gitignore`。
- [x] **3. `go mod tidy`** ✅ 已跑过：无变化（依赖本就干净）。`lxn/walk` **仍需保留**——
      `modules/preferences/panel_windows.go` 与 `modules/repair/panel_windows.go` 在用。
- [x] **4. 确认 `internal/server/` 的作用** ✅ 已落地：面板服务端已接入 `app`。
      `internal/server` 提供 JSON REST（`/api/state`、`/api/modules[/id]`、`/api/app`、`/api/events` SSE）+ `embed`
      内置的 `web/index.html` 单页面板；`internal/app/provider.go` 实现 `server.Provider`
      （依赖方向 `app -> server`，反向会成环）。`App.StartPanel()` 仅监听 `127.0.0.1`，端口取自 `app.server_port`
      （0 = 由 OS 分配），绑定失败只告警不 fatal。

## P2 — 功能补齐（规划中，见 README）

- [x] **5. 托盘接线** ✅ 已落地：`App` 新增 `tray` 字段，`StartTray()` 创建图标并
      `refreshTrayMenu()` 动态生成菜单（打开面板 / 模块开关勾选 / 打开各模块窗口 / 开机自启 / 退出）。
      菜单在启停模块、改设置、面板就绪后自动刷新；`Shutdown` 先销毁图标再停模块。
      Windows 端 `App.Run()` 跑 `winui.MessageLoop`（托盘回调依赖消息泵），非 Windows 退化为等待 ctx。
- [x] **6. 开机自启** ✅ 已落地：`sysutil.SetAutostart/IsAutostart` 原本已实现（Windows 写
      `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，Linux 写 `~/.config/autostart/*.desktop`），
      此前**无人调用**。现由 `App.applyAutostart()` 统一承接，三条路径均已接线：
      启动时 `App.SyncAutostart()` 对齐配置与系统状态、托盘菜单"开机自启"开关、面板 `PATCH /api/app`。
      macOS 的 launchd 方案仍未实现（Linux 走 .desktop，Windows 走注册表）。
- [ ] **7. 自动更新**：代码中**完全没有**。规划用 GitHub Releases API 比对版本 + `go-github-selfupdate` 类库，
      托盘菜单加"检查更新"入口。
      **前置条件已完成（2026-09-24）**：`internal/app.Version` 由 `const` 改为 `var`，
      `scripts/build.sh` 用 `-X` 注入；取值优先 `PCM_VERSION`（CI 设为 `github.ref_name`），
      否则 `git describe`，未打 stamp 时回退内嵌 build info、再兜底 `0.0.0-dev`。
      因此任何构建都有非空版本号，版本比较有可信基准。
- [~] **8. 首选项面板迁移到 Wails**：**已按"并存"方案落地原生窗口骨架**（2026-09-24）。
      新增 `internal/wailsapp`（`_windows.go`/`_other.go` 成对，Wails v2.10.2/WebView2），
      Windows 启动时在独立 `LockOSThread` 线程开启原生窗口（托盘消息泵仍占 main，互不抢占）；
      非 Windows 返回 `ErrUnsupported`，继续用 HTTP 面板。
      **单一数据源**：面板前端从 `internal/server/web/` 迁到 `internal/panel/index.html`，
      HTTP 与原生窗口共用同一文件；前端新增 `transport` 抽象，自动选择
      `fetch+EventSource`（HTTP）或 `window.go.wailsapp.API` + `runtime.EventsOn`（Wails）；
      二者共用 `server.Provider`（经 `App.PanelProvider()`），无重复业务逻辑。
      `lxn/walk` 面板（preferences/repair）按约定**暂不动**，待原生窗口稳定后再替换。
      **未验证**：真实 Windows 上 WebView2 窗口能否正常渲染与交互（需 WebView2 运行时）；
      若运行时缺失会回退到浏览器面板。
      - [ ] 建 `frontend/` 后**必须**加 emoji 检查（正则 `[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}]`）——
            客户端界面禁用 emoji，一律用 icon 资源替代。
- [ ] **9. 托盘图标资源**：`defaultIcon()` 目前硬编码回退 shell 通用图标，无自定义图标文件、无配置项。
      需补图标资源与（可选）配置化路径，并与前端 icon 规范一致。

## P3 — 卫生与规范

- [x] **10. 补测试** ✅ 已落地（第一批）：`internal/core`（Bus 投递/历史重放/慢订阅不阻塞/Close 幂等、
      Registry 顺序与去重/并发注册、热键解析规范化与非法输入、HotkeyManager 无效热键告警）与
      `internal/config`（默认值、`selfcontext` 默认关闭、Set 持久化并重载、旧配置回填、原子写无残留、
      `UpdateApp` 持久化、模块 ID 排序）与 `internal/server`（`httptest` 冒烟：嵌入式面板、REST 读写、
      SSE 实时投递、真实 `Start()` 绑定回环端口）共 **38 个用例**，`go test -race -count=1 ./internal/...` 全绿。
      后续仍应补 `modules/*` 的单测。
- [ ] **11. 命名不一致**：代码内部代号 `GoBox`（`paths.AppName`、`tray` 窗口类名 `GoBoxTray`、日志 `gobox.log`、
      `internal/core` 注释）与仓库名 `PCMannager` 并存。文档已统一用 PCMannager，**代码待改**。
- [ ] **12. 模块目录名/包名核对**：迁移后应为 `taskbar`/`repair`（原 `statusbar`/`pcrepair`），
      已改但需确认无残留引用；`preferences` 非模块（注册表视图）。
- [ ] **13. `modules/*` 中仍存在的平台差异**：确认为 `_windows.go`/`_other.go` 成对且签名一致，
      非 Windows 端不得 import `walk`/`w32`/`systray`/`gohook`。
- [x] **14. 电脑修复工具箱完善**：`modules/repair/catalog.go` 已抽成声明式目录（7 大页、~60 工具：
      Windows 设置/网络排查/系统清理/浏览器/安全软件/开发工具/WSL/.NET/git/node/python/vscode/sublime/winget/choco/
      网络诊断工具），`Actions()` 全量暴露、`RunAction` 经 `Lookup`+`ResolveCommand` 分派、
      walk 面板与 Web 面板同源渲染；`catalog_test.go` 12 例覆盖必备工具/危险标记/winget↔choco 回退。
- [x] **15. `modules/*` 单测补齐**（承接第 10 项遗留）：已完成
      `screenshot`（格式/保存目录/历史上限/文件头魔法字节/State 回显）、
      `clipboard`（History 顺序、上限裁剪、Resize、去重、Delete/DeleteExcept/Clear、PruneOlder、
      并发 AddText 与混合操作 race 验证）、
      `taskbar`（humanScale 档位边界、Collector interval 钳制/采样/Stop 不泄漏、
      Options 覆盖 25 个 opt*、Actions、toInt/round1/pct/rate、State 字段）。
      **仍缺** `preferences` 的单测。
- [ ] **18. 消除 `[restart]` 元数据 hack** ✅ 已修复：原先 `app.go` 用
      `strings.HasPrefix(o.Help, "[restart]")` 判断改配置后是否需重启模块——改一次帮助文案
      就会静默丢失重启语义，且面板上会显示 `[restart]` 脏字符。已新增
      `core.Option.Restart bool` 字段，迁移 4 处声明（clipboard 记录图片、taskbar 布局/渲染、
      selfcontext 采样间隔），`app.go` 改为读 `o.Restart`。
- [x] **16. emoji 硬约束自动化**：新增 `scripts/check-emoji.sh`（扫描 html/css/js/md/go，
      正则覆盖 emoji 与符号区段），本地已跑通；已接入 CI——`.github/workflows/release.yml`
      新增独立 `lint` job（运行该脚本），`build` 通过 `needs: lint` 串在其后，
      因此 emoji 违规会直接卡住发布，且不会随 4 个平台重复执行 4 次。
- [x] **17. 统一面板配置回显 + 操作分组**：`internal/app/provider.go` 的 `moduleInfo()` 会把配置中
      **当前生效的 option 值**合并进 `State.options`（此前各模块 `State()` 不含 options，
      导致面板表单永远显示声明式默认值、保存后看不出当前值）。
      前端 `web/index.html` 已按 `Action.Group` **分组渲染**操作按钮（repair 约 60 个工具
      不再平铺成一堵墙），并新增 `.card.sub` / `.btn.install` 样式。

## 交接须知

- 克隆后必须 `git submodule update --init --recursive`（`reference/` 是 submodule，含 5 个第三方仓库，不参与主构建）。
- 本地调试：`PCMANNAGER_CONFIG=/path/to/config.yaml go run .`。
- 模块开发前**必读** [`docs/MODULE-CONTRACT.md`](docs/MODULE-CONTRACT.md)（接口、12 条硬性规则、验证命令）。
- 构建/调试命令见 [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)；架构与开发约定见 [`AGENTS.md`](AGENTS.md)。
- 历史演进见 [`CHANGELOG.md`](CHANGELOG.md)。
