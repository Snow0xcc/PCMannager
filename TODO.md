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
      **前置交付已落地**：`docs/RELEASE-CHECKLIST.md`（可勾选清单：机械发版步骤 + 实机验收
      2.1–2.8 + 发版记录模板，2026-10-04）；本项保持未勾选——验收本身必须等真实 Windows 机器执行后留档。

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

## 真实 Windows 实机反馈（第二轮，2026-09-27）

针对实机使用反馈修复的问题（均已实测验证）：

- [x] **22. 截图编辑器全屏黑屏（关键）**：`winui.Canvas.Image` 用 `StretchDIBits` 接收
      **Go 堆**缓冲区，源图一大就静默失败（实测 512×512 成功、768×768 起返回 0）。
      而截图编辑器正是把全屏捕获拉伸到窗口绘制，故整屏显示为纯黑。
      现改用 `CreateDIBSection`（Windows 持有像素）+ `BitBlt`/`StretchBlt`，并新增
      `ImageSubRect`（选区原图重绘）与 `FillAlpha`（半透明遮罩）。
      回归测试 `internal/winui/draw_windows_test.go`（含 1920×1080 全屏尺寸用例）。
- [x] **23. `AlphaBlend` 传参错误**：`BLENDFUNCTION` 是 4 字节结构体，x64 下**按值**传
      （打包进寄存器），传指针会让调用静默失败——截图遮罩、任务栏半透明都受影响。
      另修正其所在 DLL（`msimg32`，非 `gdi32`）。
- [x] **24. 截图编辑器交互升级**：去掉了屏幕边缘的固定底栏，改为飞书/Snipaste 风格的
      **选区下方浮动工具栏**（确认 / 复制 / 取消），并实时显示选区尺寸；未框选时提示
      “拖动鼠标框选区域”并保留半透明桌面可见（不再整屏遮死）。
- [x] **25. 剪贴板窗口不可移动**：`WS_POPUP` 无标题栏，新增 `winui.BeginDragWindow`
      （`ReleaseCapture` + 合成 `WM_NCLBUTTONDOWN`/`HTCAPTION`）；窗口改为居中显示，
      并在标题行提示“按住标题栏可拖动窗口”。
- [x] **26. 任务栏文字颜色自适应**：新增 `winui.ContrastText`（Rec.709 亮度阈值），
      背景为浅色主题色时自动改用黑色文字；新增 `auto_fg_color` 开关（默认开），
      关闭后回退到手选的 `fg_color`。
- [x] **27. 任务栏新增电量显示**：`winui.PowerStatus` 读 `GetSystemPowerStatus`，
      新增 `show_battery` 开关（默认关）；无电池设备（台式机/VM）自动隐藏，不显示误导性的 0%。
- [x] **28. 日志双写 + 兜底**（承接第一轮）：新增 `paths.ExecutableLogFile`，日志同时写入
      **程序所在目录** `logs/gobox.log`；`logx` 用 `bestEffort` 多路写入器替换
      `io.MultiWriter`（后者在 GUI 子系统下因 stderr 无效而连带丢掉文件日志）。
- [x] **29. `SetBkMode`/`SetTextColor` 挂错 DLL**：从 `user32` 解析会 panic，导致任务栏
      小组件首次绘制时崩掉整个进程；已改到 `gdi32`。
- [x] **30. 热键 `close()` 死锁**：提前返回分支无界等待 `doneCh`，而它只由 `run()` 关闭，
      “未 Start 就 Stop”时 `Stop()` 永久挂起；已抽出带超时的 `join()`。
- [x] **31. 构建缺 `-tags production`**（第一轮根因）：Wails 退回桩实现，
      `runtime.EventsEmit` 触发 `log.Fatalf` 导致 GUI 构建静默退出；`scripts/build.sh` 已固定。

**仍待跟进**：`modules/repair` 的 walk 面板创建失败（`TTM_ADDTOOL failed`，属既有缺陷，
日志已捕获——正是本轮日志能力发挥作用的例证）。

## 真实 Windows 实机反馈（第三轮，2026-09-27）

针对任务栏小组件问题的修复（均已实机验证）：

- [x] **32. 任务栏卡死（关键）**：组件是 Explorer `Shell_TrayWnd` 的**跨进程子窗口**，
      而 `widget.Run()` **缺少 `runtime.LockOSThread`**。goroutine 被 Go 调度器换线程后，
      消息泵不再跑在窗口所属线程上，Explorer 在同步 `SendMessage`（`WM_PAINT` 等）里
      **永久阻塞 → 整个任务栏卡死**。对比：`clipboard`/`screenshot` 的窗口早就锁了线程，
      只有 taskbar 漏了。
- [x] **33. 组件完全不可见**：Windows 11 任务栏内有一个铺满全条的 XAML 合成层
      `Windows.UI.Composition.DesktopWindowContentBridge`，Z 序在普通子窗口**之上**。
      实测证据：`IsWindowVisible=true` 但 `WindowFromPoint` 返回 0、截屏只见任务栏。
      新增 `winui.BringToTop`（`SetWindowPos` + `HWND_TOP`），并在每次定时器 tick 重新提升。
- [x] **34. 位置压在时钟上**：原定位 `x = 任务栏宽度 - 组件宽 - offset`（贴右边缘），
      而 `TrayNotifyWnd`（时钟/托盘）实测占 `1111..1493`（任务栏宽 1493），
      组件落在 1285 —— 正好压在时钟上。新增 `winui.TaskbarMetricsNow()` 读 `TrayNotifyWnd`
      的**左边界**，组件锚定其左侧（TrafficMonitor 式）；托盘宽度随图标增减变化，
      故用独立定时器（2s）重定位。
- [x] **35. 背景不透明/文字看不清**：改为**真透明背景 + 加粗反色深色文字**：
  - GDI 子窗口不合成，不画背景不会露出父窗口，故用 `WS_EX_LAYERED` + `LWA_COLORKEY`
    （新增 `winui.SetColorKey`），背景填充色键 => 任务栏自身的像素直接透出。
  - 但 **ClearType 亚像素边缘是与背景的混色**而非精确键值，会残留品红描边；
    故透明模式必须用 `winui.NewFontQuality(..., NONANTIALIASED_QUAL)`（硬边缘）。
  - 文字取**背景反色**（`winui.ContrastText`/`IsLightColor`，Rec.709 亮度）+ `FW_BOLD`。
  - 采样点改到组件矩形**之外**（否则读回自己的键色）。新增测试守护反色契约。
- [x] **36. 电量显示**：`winui.PowerStatus`（`GetSystemPowerStatus`），`show_battery` 开关；
      无电池设备自动隐藏。（gopsutil v3.24.5 无电池 API，故自研。）
- [x] **37. `SetBkMode`/`SetTextColor` 挂错 DLL**：从 `user32` 解析会 panic 导致进程崩溃，已改 `gdi32`。
- [x] **38. 热键 `close()` 死锁**：“未 Start 就 Stop”时无界等待 `doneCh`，已加超时。

## 真实 Windows 实机反馈（第四轮，2026-09-27）

针对剪贴板粘贴、任务栏排版与截图标注的修复：

- [x] **39. 剪贴板“选一条无法粘到目标窗口”**：真因不在剪贴板库（实测 `Read/Write/Watch` 均正常，
      历史也能正确记录外部复制），而在**焦点归属**：查看器窗口在屏时会持有前台，写回后
      直接关闭又不把焦点还回去，于是要么没有目标窗口、要么粘到已关闭的查看器上。
      修法：`showViewer` 打开前记住 `winui.FocusedWindow()`，`writeBackSelected` 写回后关闭窗口
      并由新增的 `restoreFocus`（等窗口真正消失后）恢复焦点；`paste_on_copy` 开启时再发 Ctrl+V，
      关闭时用户手动 Ctrl+V 也能直接落到目标窗口。新增 `winui.SetForegroundWindow/FocusedWindow`。
- [x] **40. 任务栏窗口潦漏（文字重叠的真正原因）**：实测发现同一位置堆了 **3 个 GoBoxTaskbar 窗口**。
      两个原因：① `Stop()` 从模块 goroutine 调 `DestroyWindow`（必须同线程），静默失败；
      ② `rebuildWidget` 的 stop+start 无互斥，并发时会丢掉旧窗口引用。
      修法：`WM_CLOSE` 投递到窗口自己线程销毁 + 有界等待；新增 `rebuildMu` 串行化重建；
      `spawnWidget` 先 `reapStaleWidgets()` 清理残留（含新增 `winui.ChildWindows`）。
      实测：并发 6 次重建后仍只有 1 个窗口。
- [x] **41. 任务栏字号与排版**：默认字号 9→**11**（上限放宽到 32），宽度默认 200→240；
      两行排版按**字体实测高度**分配行高并居中，避免大字号时两行重叠；按**测量宽度**
      均衡拆分字段（网络速率字段明显更宽），不再简单地按个数二分。
- [x] **42. 截图标注（飞书式）**：新增矩形/椭圆/箭头/画笔、颜色与粗细选择、撤销（按钮 + Ctrl+Z）。
      标注存于窗口坐标并按需重放，导出时用新增的 `winui.RenderOverlay` 合成进裁剪图；
      选区下方浮动工具栏扩展为 8 个按钮，上方新增样式条。

## 真实 Windows 实机反馈（第五轮，2026-09-27）

录屏与滚动截图落地，并修复三处实测缺陷：

- [x] **43. 录屏（GIF）**：`Alt+Shift` 无关，面板动作「录屏 (GIF)」打开编辑器进入框选模式，
      工具栏为「开始录制 / 取消」；录制中浮现独立置顶控制条（停止并保存 / 丢弃）。
      实现为 `modules/screenshot/recorder.go`（`frameEncoder`，共享 Plan9 调色板、帧上限 1200）
      + `record_flow.go`（采样循环）。**格式仅 GIF**：纯 Go 无成熟 H.264 编码器，
      项目约束零 cgo + 无 ffmpeg，MP4/音频/摄像头/麦克风均无法实现。
- [x] **44. 滚动截图**：框选后手动滚动或自动滚动（注入滚轮），条带匹配拼接为长图。
      算法在 `scroll.go`：从上一帧位置**向外辐射**搜索条带（纯色/重复行动内容有多解，
      全帧扫描会选错位置），容差 4、最大高度 20000、连续 4 帧无新增即判定到底。
      自动滚动会把光标移到选区中心再 `SendInput` 滚轮（新增 `winui.SetCursorPos/ScrollWheel`），
      属“控制用户电脑”行为，已在日志与面板提示中说明。
- [x] **45. 截图/录屏编辑器窗口遮挡与焦点**：
  - 编辑器窗口本身会盖住选区，故录制/滚动开始前 `parkEditor()` 隐藏它；
  - 控制条是**另一个窗口**（`WS_EX_TOPMOST|WS_EX_TOOLWINDOW`），与编辑器同线程，
    `teardown` 里一并销毁（`DestroyWindow` 跨线程会静默失败）；
  - 编辑器轮询 `captureStatus()` 识别“自然结束”（录屏到帧上限、滚动到底），自动收尾。
- [x] **46. 非 Windows 降级成对补齐**：`editor_other.go` 的 `openEditor` 签名随模式/hooks 变化，
      `input_other.go` 新增 `ScrollWheel`/`SetCursorPos` 空实现——否则 linux/darwin 构建会断。
- [x] **47. 版本号注入失效（影响自动更新）**：`internal/app.Version` 原本是
      `var Version = versionFromBuildInfo()`，**包初始化器在链接期 `-X` 之后运行**，
      把注入的 tag 覆盖回内嵌 build info（实测日志显示 `v0.1.0-rc1.0.<日期>+dirty`
      而非构建时的 `v0.1.0-rc1-8-gd412d3e-dirty`）。
      修法：`var Version = devVersion` + `init()` 里“仅当仍等于 devVersion 才回退”。
      实测注入 `PCM_VERSION=v9.9.9-test` 后日志正确显示 `v9.9.9-test`。
      这是 #7 自动更新的前置条件：比较基准错了会导致误判“有新版本”。
- [x] **48. 剪贴板面板动作无法自动粘贴**：`Feature.writeBack` 把 `winui.Invalid` 当粘贴目标
      传给 `sendPaste`，后者校验目标无效直接返回错误 → 「写回最近一条」即使开启
      `paste_on_copy` 也永远粘贴失败。修法：开启时取 `winui.FocusedWindow()`（写回前捕获）。
- [x] **49. 任务栏字号偏小、排版拥挤（根因）**：网格的数值域/单位域/标签域/电池图标原本是
      **固定像素常量**，只在某个字号下对齐；把字号调大后字段还不够宽，文字互相挤压。
      改为按**字体实测**推导（`measureGrid` → `computeGridMetrics`），字段与行高随字号缩放，
      默认字号 11→**13**、宽度 200→**230**（配置里写死的旧值仍可通过面板改回）。
      新增 `grid_windows_test.go` 守护“字段随字号增长 / 不小于内容 / 零测量不塌陷”。

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
      内置的 `internal/panel/index.html` 单页面板；`internal/app/provider.go` 实现 `server.Provider`
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
- [~] **7. 自动更新**：**已实现为 updater 模块**（2026-09-24），零第三方依赖（不用 go-github-selfupdate）。
      `modules/updater` 实现 `core.Module`：定时检查 GitHub Releases（`auto_check` 默认开、
      `interval_hours` 1-168h）、`include_prerelease` 开关（SemVer 比较：final > rc，
      rc→更新 rc 需 opt-in）、发现新版经 Bus 事件 + 系统通知提醒。
      面板动作三步走：`check_now` 检查 → `download` 下载平台匹配资产到 DataDir 暂存
      （原子写 + sha256 + 128MiB 上限 + 主机白名单：仅 api.github.com/github.com/
      objects.githubusercontent.com）→ `apply_update`（danger+admin）显式应用：
      写 PowerShell helper（等进程退出→备份→替换→重启）经 `sysutil.RunElevated` 提权执行。
      非 Windows 走 shell 等价路径；root 下直接 swap+syscall.Exec。
      `core.AppControl` 新增 `Version()`（*App 返回 app.Version），模块由此拿到可信版本基准。
      默认**模块本身关闭**（`enabled: false`），需用户在面板显式开启。
      **未验证**：真实替换流程（等进程退出→swap→重启）只能在 Windows 实机验证；
      主机白名单在代理/企业网环境可能拦截下载（设计如此，安全优先）。
      - [ ] Windows 实机验证 apply_update 全链路（HANDOVER 6.4 补充）
      - [ ] 托盘菜单加"检查更新"入口（现仅面板动作）
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
      迁移设计已建 `docs/NAMING-MIGRATION.md`（全量盘点表 + 迁移方案 + 不迁移的后果 + 建议排期），
      **执行待排期**——建议在自动更新（#7）Windows 实机验证完成、且 updater 的
      `update.log` 硬编码路径（`feature.go:551`，与 `paths.AppName` 双端耦合）改为参数传入之后，
      单独一个 PR 执行。改名即等同重置用户配置，必须带迁移逻辑，不可裸改。
- [ ] **12. 模块目录名/包名核对**：迁移后应为 `taskbar`/`repair`（原 `statusbar`/`pcrepair`），
      已改但需确认无残留引用；`preferences` 非模块（注册表视图）。
      **对账完成**：代码零残留（`grep "statusbar"|"pcrepair"` 无命中，8 个模块包名均等于目录名，
      `preferences` 确认未在 `main.go` 注册）；陈旧文档陈述已修
      （`AGENTS.md`、`README.md`、`.rustcode/skills/new-module/SKILL.md`）。
      结论可关闭。
- [ ] **13. `modules/*` 中仍存在的平台差异**：确认为 `_windows.go`/`_other.go` 成对且签名一致，
      非 Windows 端不得 import `walk`/`w32`/`systray`/`gohook`。
      **对账完成，三项检查全绿**：成对文件交集签名 0 不一致；中立代码实际调用的成对函数
      0 不一致；非 Windows 端违禁 import 0（`repair/panel_other.go` 命中的是注释 "walk cannot
      render"）。6 个仅 Windows 有的文件均只定义小写私有符号且仅被 Windows 文件引用
      （Linux 构建通过即为证），无需补 `_other` 空文件。核对表见
      `.superpowers/sdd/ROADMAP/task-4-report.md`。结论可关闭。
- [x] **14. 电脑修复工具箱完善**：`modules/repair/catalog.go` 已抽成声明式目录（7 大页、~60 工具：
      Windows 设置/网络排查/系统清理/浏览器/安全软件/开发工具/WSL/.NET/git/node/python/vscode/sublime/winget/choco/
      网络诊断工具），`Actions()` 全量暴露、`RunAction` 经 `Lookup`+`ResolveCommand` 分派、
      walk 面板与 Web 面板同源渲染；`catalog_test.go` 12 例覆盖必备工具/危险标记/winget↔choco 回退。
- [x] **15. `modules/*` 单测补齐**（承接第 10 项遗留）：已完成
      `screenshot`（格式/保存目录/历史上限/文件头魔法字节/State 回显）、
      `clipboard`（History 顺序、上限裁剪、Resize、去重、Delete/DeleteExcept/Clear、PruneOlder、
      并发 AddText 与混合操作 race 验证）、
      `taskbar`（humanScale 档位边界、Collector interval 钳制/采样/Stop 不泄漏、
      Options 覆盖 25 个 opt*、Actions、toInt/round1/pct/rate、State 字段）、
      **`internal/app`** ✅（2026-09-24 补齐，此前零覆盖，而它是 P0 托盘/热键/Bus 修复的调用方）：
      注册去重与默认值回填、Enable/Disable 往返、`ApplyOption` 的 Restart 与非 Restart 语义、
      无效热键拒绝且不落盘、`LastError` 记录与清除、Shutdown 幂等、模块注册顺序、`Version` 非空；
      **`modules/preferences`** ✅（TODO #15 点名的最后一块）：nil Manager 容错、
      ModuleIDs 顺序与去重、仅存在于配置的模块仍出现、`Show` 在非 Windows 与 nil 下均不 panic。
      全仓用例数以实测为准（`grep -rn "^func Test" --include=*_test.go internal/ modules/ | wc -l`，勿手写具体数字），`go test -race -count=1 ./internal/... ./modules/...` 全绿。
      **仍缺**：`internal/tray`（Windows build-tag 仅验证可编译）、`internal/winui`、
      `internal/logx`、`internal/paths`、`internal/wailsapp`（均为平台 UI/IO，收益低）。
- [x] **18. 消除 `[restart]` 元数据 hack** ✅ 已修复：原先 `app.go` 用
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
      前端 `internal/panel/index.html` 已按 `Action.Group` **分组渲染**操作按钮（repair 约 60 个工具
      不再平铺成一堵墙），并新增 `.card.sub` / `.btn.install` 样式。

## 交接须知

- 克隆后必须 `git submodule update --init --recursive`（`reference/` 是 submodule，含 5 个第三方仓库，不参与主构建）。
- 本地调试：`PCMANNAGER_CONFIG=/path/to/config.yaml go run .`。
- 模块开发前**必读** [`docs/MODULE-CONTRACT.md`](docs/MODULE-CONTRACT.md)（接口、12 条硬性规则、验证命令）。
- 构建/调试命令见 [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)；架构与开发约定见 [`AGENTS.md`](AGENTS.md)。
- 历史演进见 [`CHANGELOG.md`](CHANGELOG.md)。
