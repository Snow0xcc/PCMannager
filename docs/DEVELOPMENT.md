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

# 构建（当前平台）
go build -o pcmannager .

# 交叉构建 Windows 可执行文件（无需 CGO）
CGO_ENABLED=0 GOOS=windows go build -o pcmannager.exe .

# 静态检查
go vet ./...

# 格式化检查 / 修复
gofmt -l .          # 列出未格式化文件
gofmt -w .          # 就地格式化

# 运行（开发期直接跑主程序）
go run .
```

Windows 下可用 PowerShell 等价命令；macOS/Linux 已验证可编译核心逻辑（GUI 部分在非 Windows 下降级为空转）。

## 4. 开发工作流

1. **改代码前**先 `git submodule update --init --recursive` 确保参考仓库在位。
2. 新增模块：在 `modules/<name>/` 下建包实现 `internal/core.Module`，并在 `main.go` 注册；平台相关 UI 须成对提供 `_windows.go`（`//go:build windows`）与 `_other.go`（`//go:build !windows`）。
3. 改动依赖后跑 `go mod tidy`，并提交更新后的 `go.mod`/`go.sum`。
4. 提交前执行 `gofmt -w . && go vet ./...` 自查。
5. 前端（规划中的 Wails 面板）**严禁使用 emoji**，图形一律用 icon 资源替代（详见 README「前端显示规范」）。

## 5. 本地运行与调试

- 配置文件默认位于用户配置目录下的 `PCMannager/config.json`。可用环境变量覆盖路径以便调试：

  ```bash
  PCMANNAGER_CONFIG=/path/to/dev-config.json go run .
  ```

- 日志写入配置文件同级的 `pcmannager.log`。

## 6. 已知构建缺口（重要）

当前 `main.go` 仍引用旧版 `core` API（`Manager`/`Feature`/`App`），而 `internal/core` 已重构为 `Module`/`Registry`/`Bus` 体系，二者尚未对齐，**直接 `go build` 会失败**。

在迁移缺口修好之前：

- CI（`.github/workflows/release.yml`）在构建步骤会报错，属预期。
- 本地无法完整 `go run .`。若需先跑通，需先把 `main.go` 与 `internal/core` 的新接口对齐（这是独立的迁移任务，不在普通功能开发范围内）。

## 7. 提交说明

- 提交信息使用 Conventional Commits 风格（如 `feat:`、`fix:`、`docs:`、`chore:`）。
- 若改动涉及 `reference/` 子模块版本，请单独提交 `.gitmodules` 与子模块指针变更，并注明所引用的上游 commit。
- 不要将 `bin/`、`*.exe`、本地日志提交进仓库（已由 `.gitignore` 排除）。
