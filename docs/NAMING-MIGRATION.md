# GoBox → PCMannager 命名迁移设计

> **状态：仅设计，未执行任何改名。** 本文件是 TODO #11 的交付物，对应
> `docs/ROADMAP.md`「明确不做」清单中的"不动 GoBox → PCMannager 代码改名"。
> 执行需排期，见文末「建议排期」。

规划代号 `GoBox` 与产品名 `PCMannager` 指代同一产品。文档层已统一用 PCMannager
（README/CHANGELOG/ROADMAP 均如此），代码层仍留 `GoBox`。

## 1. 现状盘点

### 1.1 运行时身份（改了会改变程序行为）

| # | 位置 | 现值 | 作用 | 改动风险 |
|---|---|---|---|---|
| 1 | `internal/paths/paths.go:12` | `AppName = "GoBox"` | **数据目录名**：Windows `%APPDATA%\GoBox`、Linux `$XDG_CONFIG_HOME/GoBox`；也是 `os.UserConfigDir` 失败时的 `os.TempDir()/GoBox` 兜底 | **最高**——直接决定配置/历史/日志的落盘位置 |
| 2 | `internal/paths/paths.go:15` | `LogFileName = "gobox.log"` | 数据目录与程序目录两处日志文件名 | 高——改名后旧轮转备份成孤儿，用户按文档找不到日志 |
| 3 | `internal/winui/window_windows.go:454` | `fmt.Sprintf("%s_GoBox_%d", className, nextClassID())` | **所有窗口类名的实例后缀**（`Foo_GoBox_N`） | 高——`ChildWindows(parent, prefix)` 靠前缀清扫陈旧窗口（见 #4） |
| 4 | `modules/taskbar/feature.go:468` | `widgetClassPrefix = "GoBoxTaskbar"` | `platform_windows.go:51,61` 用它清扫上一轮遗留的任务栏小组件窗口（与 #8 的 `GoBoxTaskbar` 是**同一字符串的两种用途**，改名须同批改） | 高——改名后新版本扫不到**旧版本**进程留下的窗口，AGENTS.md 记载的"文字重叠堆叠 3 个窗口"故障会复发 |
| 5 | `internal/tray/tray_windows.go:170` | `"GoBoxTray"` | 托盘隐藏窗口类名 | 中——跨版本共存时无法按类名识别旧托盘窗口 |
| 6 | `modules/updater/feature.go:551` | PS helper 内 `$env:APPDATA 'GoBox\logs'` | 更新完成记录 `update.log` 的**硬编码**写入路径 | 中——**与 #1 是双端耦合**：读取端 `updateDoneLog()` 走 `paths.DataDir("")`，改 #1 而漏改此处 → 写读路径分叉，更新完成永远不被消费 |
| 7 | `modules/updater/proxy_test.go:91` | `"C:\\Program Files\\GoBox\\config.yaml"` | 测试夹具（仅作为含空格路径样本） | 低——夹具，语义无关 |
| 8 | 全部窗口类名 11 个（含 #5 单列的 `GoBoxTray`，此处列其余 10 个） | `GoBoxClipboard`、`GoBoxLauncher`、`GoBoxSuperPanel`、`GoBoxCaptureCtrl`、`GoBoxScreenshot`、`GoBoxPin`、`GoBoxContext`、`GoBoxTaskbar`、`GoBoxInputDialog`、`GoBoxTrayTest` | 各窗口的 `RegisterClassW` 类名（含测试窗口类） | 中——同 #5，跨版本共存/查找 |
| 9 | `internal/tray/tray_windows.go:391`、`modules/taskbar/widget_windows.go:466` | `tip = "GoBox"`、`parts = []string{"GoBox"}` | 托盘 tooltip、任务栏小组件文字回显 | 低——纯显示文本 |

**已无耦合**：单实例互斥量是 `Local\pcmannager`（`main.go:42`），不是 GoBox。
配置文件名是 `config.yaml`（`config.go:174`），不含代号。

### 1.2 注释与文档（改了不改行为）

共 40+ 处，集中在 `internal/core`、`internal/logx`（轮转注释 `gobox.log.1..N`）、
`internal/tray`、`internal/winui`、`internal/app`、`main.go:1`、`scripts/build.sh:46`、
`.gitignore:11`、`AGENTS.md`、`README.md`、`docs/*`、`TODO.md`。
这些跟随 1.1 一起改即可，无独立风险。

## 2. 迁移风险与方案

### 2.1 核心风险：改名即"丢失"用户数据

`AppName` 一旦从 `GoBox` 改成 `PCMannager`，新版本启动时 `os.UserConfigDir()/PCMannager`
是**空目录**，`config.Load` 走默认值生成新配置——用户会看到：

- 首选项全部回到默认（自启、热键、模块开关、`data_dir` 覆盖全丢）
- 剪贴板历史、截图历史、自上下文记录、任务栏设置全部"消失"（数据仍在旧目录，只是没被读）
- 旧日志不再更新，排障时按文档打开新目录看到空的

由于 ROADMAP「明确不做」已把本项排除，**当前不做迁移是安全的**：不改名 = 无风险。

### 2.2 建议迁移方案（若执行）

一次性迁移，只在首次启动时跑一次，幂等、可回滚：

```
新版本首次启动（检测到 oldRoot 存在且 newRoot 不存在或为空）:
  1. oldRoot = UserConfigDir()/GoBox
     newRoot = UserConfigDir()/PCMannager
  2. 若 oldRoot 不存在 → 跳过（全新安装）
  3. 若 newRoot 已有 config.yaml → 跳过（已迁移过，幂等）
  4. 复制（不是移动）oldRoot → newRoot
  5. 在 newRoot 写标记文件 .migrated-from=GoBox
  6. 启动后正常读 newRoot
  7. 下一次启动看到标记 → 提示"已从旧版本迁移，旧数据保留在 GoBox 目录，
     确认无误后可手动删除"——**不自动删**（用户可能还要回滚旧版本）
```

要点：
- **复制而非移动**：用户可能回滚旧版本，旧数据必须还在。
- **迁移标记落 newRoot**，避免每次启动都重扫。
- **#6（update.log 硬编码路径）必须与 #1 同批改**，否则更新完成记录写读分叉。
  建议顺手把 PS helper 里的硬编码改为从参数传入，消除这类双端漂移的可能。
- **#4（`widgetClassPrefix`）改名时要新旧前缀都扫**：清扫逻辑同时匹配
  `GoBoxTaskbar` 与新前缀，过渡一两个版本再删旧前缀——否则升级当次的陈旧窗口
  没人回收，重现"文字重叠"故障。
- 日志改名（#2）：迁移时把旧 `gobox.log*` 一并复制，或在新配置里保留旧文件名
  一个版本周期。

### 2.3 不迁移的后果

- **现状（不改名）**：产品名与内部代号长期并存，新贡献者需被告知"GoBox = PCMannager"
  （README 首段已有此说明）。纯认知成本，**无功能影响**。这是 ROADMAP 当前选择。
- **改名但不做迁移**：等同于强制所有老用户重置配置——不可接受。
- **改名且做好迁移**：消除认知成本，代价是一次性迁移代码 + 跨版本窗口前缀兼容
  + 一轮真实 Windows 实机验证（数据搬移、更新记录、陈旧窗口清扫三条链路）。

## 3. 建议排期

迁移属于"用户可见但不紧急"的清理，且必须能在真实 Windows 上验证三条链路，
建议**不要**与功能项并行，而是放在：

1. 自动更新（TODO #7）**完成 Windows 实机验证之后**——迁移代码自身也需要走一遍
   "旧版本 → 新版本"的更新路径，正好复用更新流程做实测；
2. 且在 updater 的 `update.log` 硬编码改为参数传入之后（#6），避免改名时漏改；
3. 单独一个 PR，只含迁移逻辑 + 改名 + 测试，便于回滚。

在此之前，本文件作为**执行蓝图**留存；`TODO.md` 第 11 项保持未完成并指向本文件。
