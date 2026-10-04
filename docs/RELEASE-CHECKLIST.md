# RELEASE-CHECKLIST — 发版前实机验收清单

> 用途：当前自动化环境为 Linux，**代码正确性在真实 Windows 上无法证伪**（ROADMAP B5）。
> 每次发版前，在真实 Windows 机器上逐项执行并打勾，同时记录机器/系统版本。
> 实测记录是发版的必要产物：没有记录的"跑过了"不算验收。
>
> 使用方式：发版时复制本文件末尾的「发版记录模板」到 `docs/records/`（或 Wiki），
> 逐项填写；历史记录与本次机器环境一一对应，便于回溯。

## 一、机械发版步骤

### 1.1 打 tag 前（本地，Linux 或 Windows 均可）

逐条执行，全部通过才允许发版：

- [ ] `bash scripts/check-emoji.sh`（前端禁 emoji 闸门，内置探测器自检）
- [ ] `gofmt -l . | grep -v ^reference/` 输出为空
- [ ] `go build ./... && go vet ./...` 通过
- [ ] `go test ./...` 全绿（本机禁用 `-race`：与 `CGO_ENABLED=0` 约束冲突）
- [ ] `go mod tidy -diff` 无输出
- [ ] Windows 编译闸门：
      `GOOS=windows go vet ./...` 通过，且
      `for d in ./internal/... ./modules/...; do GOOS=windows go test -c -o /dev/null $d; done` 无失败
- [ ] `GOOS=darwin go vet ./...` 通过
- [ ] 提交前确认工作区干净（`git status --short` 为空）

### 1.2 打 tag 后（自动链路核对）

当前链路（2026-10-04 实测通过，见 TODO #1）：推 main → `auto-release.yml` 自动
patch+1 打 tag →（因 GITHUB_TOKEN 推 tag 不触发下游，改为）显式
`workflow_dispatch` 派发 `release.yml` → 四平台构建 → Release + 镜像表 → Wiki 同步。

- [ ] `gh run list --workflow=auto-release.yml` 最新 run 为 success，日志可见
      `previous tag` / `new tag` 符合预期
- [ ] `gh run list --workflow=release.yml` 对应 tag 的 run success，四个平台 job
      （windows/amd64、linux/amd64、darwin/amd64、darwin/arm64）+ lint 全绿
- [ ] `gh release view <tag>`：4 个附件齐全且命名一致——
      `pcmannager-windows-amd64.exe`、`pcmannager-linux-amd64`、
      `pcmannager-darwin-amd64`、`pcmannager-darwin-arm64`
- [ ] 版本注入正确：下载 linux 附件，`go version -m pcmannager-linux-amd64`
      显示 `-X .../internal/app.Version=<tag>`、`-tags=production`、`CGO_ENABLED=0`
- [ ] Release 说明中的镜像下载表四行链接均指向本 tag
- [ ] Wiki 首页显示的"最新版本"已更新为本 tag（`sync-wiki.sh`，看 job log 有
      `Wiki 已同步（<tag>）`）

## 二、真实 Windows 实机验收（B5 / TODO #49）

前置：记录环境——机器型号/性能、Windows 版本（如 Windows 11 23H2）、
显示器数量与缩放比例、本机已装的全局热键占用情况（F1、Ctrl+` 等）。

### 2.1 托盘与生命周期

- [ ] 连续启动/退出 20 次：每次托盘图标正常出现、菜单可点、退出后**无残留窗口与孤儿进程**
      （任务管理器确认无 `pcmannager.exe` 残留）
- [ ] 任务管理器结束 `explorer.exe`（会自动重启）后：托盘图标恢复、任务栏小组件重新显示
- [ ] 单实例锁生效：再次启动不会出现第二个进程（第二个实例应退出或聚焦已有实例）

### 2.2 全局热键（P0-2 线程模型实机验证）

- [ ] 默认热键注册成功（面板"热键冲突可视化"页无红色冲突项）
- [ ] 真实按键能触发对应模块动作（连按 10 次无丢触发、无延迟累积）
- [ ] 占用热键（如先开一个程序占用 F1）：本程序给出明确冲突提示，而非静默失败
      （TODO #21：F1、Ctrl+` 被占用属环境冲突，设计上已降级为提示）
- [ ] 修改热键后旧绑定解除、新绑定立即生效

### 2.3 任务栏小组件（TODO #20 修复实机验证）

- [ ] 小组件嵌入 `Shell_TrayWnd` 正常显示（不遮时钟、Z 序在 XAML 合成层之上可见）
- [ ] 托盘图标增减导致托盘宽度变化后，小组件随重定位（约 2 秒定时器）
- [ ] 调大/调小字号后：文字按字体实测排版，**无字段重叠、无品红描边**（透明背景 + NONANTIALIASED_QUAL）
- [ ] 连续切换透明/不透明、明/暗主题各 5 次，无残留背景色块

### 2.4 截图三工作流与录屏

> **用户告知义务**：滚动长截图会**注入真实滚轮并移动光标**到选区中心（属"控制用户电脑"），
> 首次使用前应向机器使用者说明。

- [ ] 框选截图：F1 唤起遮罩，框选→工具栏→保存/复制均正常；复制到剪贴板后焦点回原窗口
- [ ] 彩色标注仅在裁剪模式可用，录屏/长截图模式下该按钮不可用
- [ ] GIF 录屏：录制时编辑器自动隐藏（不把自己录进去）、停止后播放时长正确、控制条停止按钮可用
- [ ] MP4 录屏（已装 ffmpeg）：出现在选项中且可录制；**未装 ffmpeg**：选项不出现/给出明确提示并可回退 GIF
- [ ] 滚动长截图：首帧捕获、条带拼接无错位；中途鼠标被移动后能正确归位体验
- [ ] 截图历史在重启后仍受 `max_history` 上限约束（无界增长已修复）
- [ ] 面板"截图历史"卡片能列出条目（C2-5，若本版本已包含）

### 2.5 剪贴板

- [ ] 剪贴板历史记录持续写入，图片/文本条目均在
- [ ] 面板剪贴板页可浏览、写回、删除（C2-1）
- [ ] **写回焦点恢复**：打开查看器前记原前台窗口，选中条目粘贴后焦点回原窗口、Ctrl+V 到位
- [ ] 单条超大图片（>8 MiB）被拒收，历史不膨胀

### 2.6 自动更新（updater）

- [ ] 面板版本号显示无 `vv` 前缀、非 `0.0.0-dev`（A2/A3 修复）
- [ ] 模块关闭时不产生任何 GitHub 请求（A4；可用防火墙日志或断网观察）
- [ ] `apply_update` 全链路：等进程退出 → 备份旧文件 → 替换 → 重启成功（需提权 UAC 一次）
- [ ] 升级后配置与历史数据完好

### 2.7 面板（HTTP 与原生窗口）

- [ ] HTTP 面板（浏览器打开带 token 的 URL）：事件日志页实时滚动（log/state/progress/notice 四类，
      含 200 条历史回放），切页签不丢日志（A1 修复）
- [ ] 日志页过滤/清空/导出三个操作可用（C2-4）
- [ ] 修改 `server_port`/`data_dir`/`log_level` 保存后出现"重启应用后生效"提示（C2-3）
- [ ] Wails/WebView2 原生窗口：渲染正常、交互正常；**WebView2 运行时缺失**时正确回退浏览器面板
- [ ] 面板各模块开关、热键、选项修改后立即生效或按提示重启

### 2.8 修复模块（repair，可选但建议）

- [ ] 面板执行任意工具：走提权通道（Admin 条目弹 UAC）、异步执行（面板不卡）、超时保护生效
- [ ] walk 原生面板与 Web 面板行为一致（同一 `runActionEntry` 通道）

## 三、发版记录模板

复制以下模板，逐项填写：

```markdown
# 发版记录 v0.X.Y（YYYY-MM-DD）

- 执行人：
- 机器 / Windows 版本 / 显示器与缩放：
- 热键占用环境（F1、Ctrl+` 等）：
- ffmpeg 是否安装：

## 一、机械步骤
- [ ] 1.1 打 tag 前本地检查（附命令输出摘要）
- [ ] 1.2 自动链路核对（附 run URL、release URL）

## 二、实机验收
- [ ] 2.1 托盘与生命周期
- [ ] 2.2 全局热键
- [ ] 2.3 任务栏小组件
- [ ] 2.4 截图三工作流与录屏
- [ ] 2.5 剪贴板
- [ ] 2.6 自动更新
- [ ] 2.7 面板
- [ ] 2.8 修复模块（可选）

## 三、发现问题与处理
（逐条：现象 → 定位 → 处理 → 是否回填测试）
```

## 维护约定

本清单随 `docs/ROADMAP.md`（B5 节）与 `AGENTS.md` 的约定同步维护；
构建命令以 AGENTS.md「构建、测试、检查、格式化」一节为准，发现二者不一致时
**以 AGENTS.md 为准并立即修正本文件**。
