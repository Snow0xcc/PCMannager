---
description: 按发布同源参数构建指定平台产物（scripts/build.sh）
---

为 `$ARGUMENTS` 构建产物，参数形如 `<goos> <goarch>`（默认 `windows amd64`），例如：

- `/build` → `windows amd64`
- `/build linux amd64`
- `/build darwin arm64`

执行：

1. `bash scripts/build.sh <goos> <goarch> dist/pcmannager-<goos>-<goarch>[.exe]`（`windows` 目标补 `.exe` 后缀）。
2. **必须**走 `scripts/build.sh`，不要手写 `go build`——脚本固定了 `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`，并对 Windows 追加 `-H windowsgui`。漏掉 `-H windowsgui` 会导致启动时弹出黑色控制台窗口，这是历史踩过的坑。
3. 需要指定版本号时用环境变量：`PCM_VERSION=v1.2.3 bash scripts/build.sh ...`（CI 设为 tag 名；缺省回退 `git describe`）。

汇报输出路径与字节数。若构建失败，完整贴出错误并从源头修复，不要绕过脚本改用裸 `go build`。
