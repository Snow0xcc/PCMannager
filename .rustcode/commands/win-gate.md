---
description: 本地跑 Windows 测试二进制编译闸门 + 跨 OS vet（提交前补上 CI 之外的 Windows 专属检查）
---

在本地补跑 CI 中 `windows-compile-gate` job 的检查，把 Windows 专属 `_test.go` 的编译缺口提前暴露。

背景：`go test ./...` 在 Linux 上跑不到 `//go:build windows` 的测试文件，托盘死锁（P0-1）、热键线程（P0-2）这类 Windows 专属 bug 恰好藏在闸门外。

依次执行：

1. **跨 OS vet**（捕获单平台 vet 看不到的 build tag 问题）：
   ```bash
   for os in windows darwin linux; do echo "== GOOS=$os =="; CGO_ENABLED=0 GOOS=$os go vet ./...; done
   ```

2. **逐包编译 Windows 测试二进制**（只编译不执行）：
   ```bash
   CGO_ENABLED=0 go list ./... | while read -r pkg; do
     dir=$(go list -f '{{.Dir}}' "$pkg")
     compgen -G "$dir/*_test.go" > /dev/null || { echo "skip: $pkg"; continue; }
     echo "compile: $pkg"
     CGO_ENABLED=0 GOOS=windows go test -c -o /dev/null "$pkg"
   done
   ```
   统计 `compiled` / `skipped`；**若 `compiled` 为 0 说明闸门空转，必须报红**，不要静默通过。

3. **依赖一致性**：`go mod tidy -diff`

4. **跨平台可构建性**（可选，较慢）：对 `windows/amd64 linux/amd64 darwin/amd64 darwin/arm64` 各跑一次 `/build` 的脚本命令验证可编译。

汇报每项的实际结果与失败包列表。全部通过才能说"闸门通过"；不要跳过 compiled==0 的空转检查。
