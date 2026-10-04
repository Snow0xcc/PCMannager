# 快速开发指南

本指南帮助贡献者在克隆仓库后快速搭建工具链、初始化子模块并开始开发。

## 1. 环境要求

- **Go 1.27+**（当前 `go.mod` 声明 `go 1.27.1`）。
- 一个支持 CGO 关闭的构建环境即可；Windows 端 GUI 走自研无 cgo 的 `internal/winui`（`syscall.LazyDLL`），**无需 CGO**，发布命令为 `CGO_ENABLED=0 GOOS=windows go build`。
- Git 2.22+（用于 submodule）。

## 2. 克隆并初始化子模块

`reference/` 下是第三方参考仓库，以 **git submodule** 形式引入（见 `.gitmodules`）。克隆后必须初始化，否则这些目录为空、且不应手动放入源码：

```bash
# 方式一：克隆时一并拉取子模块
git clone --recurse-submodules <repo-url> pcmannager
cd pcmannager

# 方式二：已克隆后补全
git submodule update --init --recursive
```

验证子模块已检出：

```bash
git submodule status   # 每个条目前应带空格或前缀，而非减号(-)
```

> 若 `git submodule status` 中某行以 `-` 开头，说明未初始化，重新运行上面的 `update` 命令。

## 3. 工具链常用命令

项目无 Makefile，统一使用 Go 原生命令（以下均排除 `reference/`）：

```bash
# 下载/整理依赖（改动 go.mod 后先跑）
go mod tidy

# 构建（当前平台）：发布类构建统一走脚本，参数与 CI 同源
bash scripts/build.sh linux amd64 dist/pcmannager

# Windows 交叉构建（无需 CGO，自动追加 -H windowsgui 与 -tags production）
bash scripts/build.sh windows amd64 dist/pcmannager.exe

# 开发期快速跑（控制台子系统，便于看日志）：注意它没有 production 标签，
# Wails 原生窗口会退回桩实现（内部有加固，不会因此退出），仅适合核心逻辑调试
go run .

# 静态检查
CGO_ENABLED=0 go vet ./...

# 格式化检查 / 修复
gofmt -l .          # 列出未格式化文件
gofmt -w .          # 就地格式化

# 重新生成品牌 SVG（改了 internal/logo 的几何后必跑）
bash scripts/gen-logo.sh
bash scripts/gen-logo.sh --check   # 只校验 docs/site/assets/*.svg 是否与几何一致
```

Windows 下可用 PowerShell 等价命令；macOS/Linux 已验证可编译核心逻辑（GUI 部分在非 Windows 下降级为空转）。

> **不要裸调 `go build` 产出发布版**：必须带 `-tags production`（Wails 需要）且 Windows 需 `-H windowsgui`（否则弹控制台）。`scripts/build.sh` 已把两者固定下来，发布请一律走它。

## 4. 开发工作流

1. **改代码前**先 `git submodule update --init --recursive` 确保参考仓库在位。
2. 新增模块：在 `modules/<name>/` 下建包实现 `internal/core.Module`，并在 `main.go` 注册；平台相关 UI 须成对提供 `_windows.go`（`//go:build windows`）与 `_other.go`（`//go:build !windows`）。
3. 改动依赖后跑 `go mod tidy`，并提交更新后的 `go.mod`/`go.sum`。
4. 提交前执行 `gofmt -w . && go vet ./...` 自查。
5. 前端（规划中的 Wails 面板）**严禁使用 emoji**，图形一律用 icon 资源替代（详见 README「前端显示规范」）。

## 5. 本地运行与调试

- 配置文件默认位于用户配置目录下的 `GoBox/config.yaml`（Windows `%APPDATA%\GoBox`）。可用环境变量覆盖路径以便调试：

  ```bash
  PCMANNAGER_CONFIG=/path/to/dev-config go run .
  ```

- 日志同时写入两处：数据目录 `logs/gobox.log` **和程序所在目录** `logs/gobox.log`（即 exe 旁边的 `logs/`，启动时自动创建）。后者对无控制台的 GUI 构建尤其重要——直接去程序目录就能看到日志。

## 6. 构建与运行的坑

历史上 `main.go` 曾引用旧版 `core` API 而无法 `go build`，该缺口已修复：现在 `go build ./...`、`go vet ./...` 与四个目标平台交叉构建都通过。

仍要注意两点（都会导致“看起来构建成功、但行为异常”）：

- **Wails 的 `production` 标签**：缺它会让 `wailsapp` 退回 `app_default_windows.go` 桩实现（`CreateApp` 弹框返回 `nil`），事件转发随后以无效 context 调 `runtime.EventsEmit`，内部 `log.Fatalf` 直接退出进程。`scripts/build.sh` 已固定该标签。
- **Windows 的 `-H windowsgui`**：缺它会在启动时弹出黑色控制台窗口。

开发期用 `go run .`（无标签、控制台子系统）调试核心逻辑是可行的；`wailsapp` 侧已加固为“拿到空 context 就停止转发”，不会因此退出进程。

## 7. 提交说明

- 提交信息使用 Conventional Commits 风格（如 `feat:`、`fix:`、`docs:`、`chore:`）。
- 若改动涉及 `reference/` 子模块版本，请单独提交 `.gitmodules` 与子模块指针变更，并注明所引用的上游 commit。
- 不要将 `dist/`、`*.exe`、本地日志提交进仓库（已由 `.gitignore` 排除）。macOS 上 Finder 生成的 `.DS_Store` 也已加入 `.gitignore`，避免在 `git status` 里堆成未跟踪文件。
