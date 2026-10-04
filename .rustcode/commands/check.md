---
description: 提交前完整检查（gofmt / vet / test / emoji）
---

对当前工作区运行提交前检查，并逐项汇报结果：

1. 格式：`gofmt -l . | grep -v ^reference/`（非空即列出待格式化文件，可用 `gofmt -w` 修复，但始终排除 `reference/` 子模块）
2. 静态检查：`go vet ./...`
3. 测试：`go test ./...`；若本次改动涉及并发相关代码，追加 `go test -race -count=1 ./internal/... ./modules/...`
4. 前端 emoji：`bash scripts/check-emoji.sh`

任何一项失败都要先修复再汇报；全部通过时明确给出"可以提交"的结论。若依赖有改动，先跑 `go mod tidy`。
