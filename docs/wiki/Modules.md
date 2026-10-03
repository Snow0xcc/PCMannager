# 模块说明

各模块在设置面板中可独立启停与配置。

## 剪贴板（clipboard）

剪贴板历史，支持文本、图片、文件流。

- 热键默认 `Ctrl+\``
- 配置项：`max_items`（上限 500）、`store_images`、`paste_on_copy`、`retention_days`（30）
- 图片/文件的高清数据不入库，只落本地缓存文件并在库中存路径（避免数据库膨胀）

## 截图（screenshot）

三种模式：区域截图、GIF 录屏、滚动长截图。

- 热键默认 `F1`；录屏 `Alt+Shift+R`；滚动截图 `Ctrl+Shift+A`
- 工具栏：矩形、椭圆、箭头、画笔、马赛克、文本、水印、撤销
- 保存：复制到剪贴板或下载到本地
- 配置项：`format`、`jpg_quality`、`copy_after`、`save_dir`、`max_history`、`record_fps`、`record_format`、`scroll_interval_ms`
- 录屏仅 GIF（纯 Go 无 H.264 编码器，项目零 cgo 且无 ffmpeg）
- 滚动截图注入真实滚轮，属"控制用户电脑"行为，需用户知晓

## 任务栏状态统计（taskbar）

在任务栏显示网速、内存、电量等。

- 热键默认 `Ctrl+Alt+T`
- 定位锚在通知区域左侧（TrafficMonitor 式），不压时钟
- 背景透明，文字取背景反色并加粗，保证与任务栏背景对比明显
- 网格字段宽度按字体实测，随字号缩放
- 配置项：`interval`、`show_download`、`show_upload`、`show_cpu`、`show_mem`、`show_disk`、`show_uptime`、`show_battery`、`align`、`offset_x`、`width`、`layout`、`font_family`、`font_size`、`fg_color`、`auto_fg_color`、`bg_mode`、`bg_color`、`follow_theme`、`separator`、`render`、`avoid_widgets`、`multi_monitor`

## 上下文记录（selfcontext）

定时截屏并结合窗口标题记录上下文，可接视觉语言模型解析画面语义。

- 默认**关闭**（涉及屏幕内容，隐私敏感）
- 配置项：`interval`、`retention_days`、`capture_mode`、`pause_on_lock`、VLM 相关（BaseURL / Model / APIKey）
- VLM 兼容 OpenAI 接口规范，可接本地 LM Studio 等

## 电脑修复（repair）

一键安装工具箱，所有条目集中在 `modules/repair/catalog.go`（声明式、单一数据源）。

- 配置项：`prefer_source`（`auto`/`winget`/`choco`）、`confirm_danger`
- 新增工具只需改 `catalog.go`，Windows 面板与 Web 面板均从该目录渲染

## 快捷面板（launcher）

Alt+Space 唤起的全局搜索面板。

- 候选来源：模块动作、模块界面入口、网页捷径、开始菜单应用
- 排序：置顶 ×10^6 + 匹配度 ×10^3 + 预设优先级 ×10 + 打开次数 ×2
- 别名/拼音缩写自动建议（`mozillazg/go-pinyin`），可在右键"编辑关键字"中修改
- 单行横向排列，滚轮横向滚动，右上角图钉常驻可点击固定
- 配置项：`hotkey`、`max_results`

## 超级面板（super）

悬浮磁贴池，Alt+P 唤起。在快捷面板右键菜单"固定到超级面板"加入条目。

- 单击执行、右键移出或清空、失焦隐藏
- 固定项以命令 key 持久化；模块停用后对应条目自动跳过

## 自动更新（updater）

检查 GitHub Releases 并下载匹配平台的资产。

- 默认**关闭**
- 镜像池轮询：ghfast、ghproxy、moeyy、直连兜底
- 下载走主机白名单（per-dial 校验，重定向同样受控）、原子暂存、sha256 校验
- 应用更新需显式确认，经提权脚本替换并重启，绝不自动执行下载物
- 配置项：`auto_check`、`interval_hours`、`include_prerelease`
