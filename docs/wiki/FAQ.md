# 常见问题

## 程序启动后创建完配置就退出

通常是构建标签缺失：Wails 相关组件需要 `-tags production`。发布构建必须走统一脚本：

```bash
bash scripts/build.sh windows amd64 pcmannager.exe
```

该脚本固定 `CGO_ENABLED=0 -trimpath -ldflags="-s -w" -tags production`，并对 Windows 追加 `-H windowsgui`。直接 `go build` 或 `go run .` 会因缺少标签而静默退出。

GUI 子系统下 stdout/stderr 不可见，排障请看日志文件（数据目录或程序目录下的 `logs/gobox.log`）。

## 任务栏小组件看不见或任务栏卡死

- 看不见：需要提升 Z 序（程序内部已处理）。Windows 11 任务栏有 XAML 合成层会遮挡普通子窗口。
- 卡死：组件是 Explorer 的跨进程子窗口，消息必须由其所属线程处理（程序内部已锁线程）。若仍卡死，重启 Explorer 或重新启用模块。

## 截图黑屏

全屏图像传输在 Go 堆缓冲区较大时会静默失败。程序已改为 `CreateDIBSection` 写入后 `BitBlt`/`StretchBlt` 传输。若仍异常，请确认显卡驱动与 DPI 设置（程序已做 DPI 感知）。

## 剪贴板无法粘贴

查看器窗口在屏时会持有前台。程序在打开前记住原焦点窗口，关闭后恢复并等待窗口消失再发送 Ctrl+V。若粘贴仍失败，检查目标程序是否以管理员权限运行（权限不同的进程间无法模拟按键）。

## 自动更新检查不到新版本

- 确认 updater 模块已启用（默认关闭）
- 版本号由构建注入；本地 `go build` 未打 stamp 时会回退为 `0.0.0-dev`，与远端比较基准可能不符
- 下载走镜像池轮询，全失败时会聚合错误信息写入日志

## 热键无效

查看面板中的热键错误槽位。常见原因是被其它程序占用（Alt+Space 尤甚），改绑即可。

另：全局热键目前仅 Windows 生效；其它平台注册会返回「当前平台不支持全局热键」，属预期行为，请改用设置面板中的动作触发功能。

## 日志在哪里

- 数据目录：`logs/gobox.log`
- 程序所在目录：`logs/gobox.log`

两者同一文件时不会重复打开。日志按 8 MiB 轮转，保留 `.1` `.2` `.3` 三个备份。

## 前端为什么没有 emoji

项目硬约束：客户端界面禁止 emoji，一律用图标资源。提交前请运行：

```bash
bash scripts/check-emoji.sh
```
