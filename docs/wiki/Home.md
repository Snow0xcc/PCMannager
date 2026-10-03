# PCMannager 文档

跨平台系统托盘工具：剪贴板历史、截图、上下文记录、电脑修复、任务栏状态、快捷面板、超级面板、自动更新。

> 本页由 CI 自动同步（`docs/wiki/Home.md` 是单一数据源），版本号与下载链接在每次发版时刷新。

**最新版本：{TAG}**

## 下载

| 平台 | 官方源 | ghfast 加速 | ghproxy 加速 |
| :--- | :--- | :--- | :--- |
| Windows (amd64) | [下载](https://github.com/{REPO}/releases/download/{TAG}/pcmannager-windows-amd64.exe) | [高速](https://ghfast.top/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-windows-amd64.exe) | [高速](https://ghproxy.net/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-windows-amd64.exe) |
| Linux (amd64) | [下载](https://github.com/{REPO}/releases/download/{TAG}/pcmannager-linux-amd64) | [高速](https://ghfast.top/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-linux-amd64) | [高速](https://ghproxy.net/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-linux-amd64) |
| macOS (amd64) | [下载](https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-amd64) | [高速](https://ghfast.top/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-amd64) | [高速](https://ghproxy.net/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-amd64) |
| macOS (arm64) | [下载](https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-arm64) | [高速](https://ghfast.top/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-arm64) | [高速](https://ghproxy.net/https://github.com/{REPO}/releases/download/{TAG}/pcmannager-darwin-arm64) |

镜像站偶发限流，某个节点不可用时请切换其它节点或官方源。

> **平台成色提示**：四个平台的产物均可构建并启动（核心、HTTP 面板与自动更新全平台可用），但功能模块的平台能力差异显著——Windows 最完整，Linux/macOS 上截图、上下文记录、修复工具箱、快捷面板等模块的核心能力不可用。逐模块明细见 [模块说明](Modules.md) 的「平台可用性」标注。

## 文档导航

- [快速开始](Quick-Start.md) — 安装与首次配置
- [模块说明](Modules.md) — 各功能模块的能力与配置项
- [快捷键](Hotkeys.md) — 全局热键与快捷面板操作
- [常见问题](FAQ.md) — 排查与常见疑问

## 自动更新

程序内置 updater 模块，默认关闭。在设置面板开启后可定时检查 GitHub Releases，下载走镜像池轮询（ghfast / ghproxy / moeyy / 直连兜底），下载后需手动确认应用。

## 构建

```bash
bash scripts/build.sh windows amd64 pcmannager.exe
```

产物名与 CI 保持一致请见 `.github/workflows/release.yml`。
