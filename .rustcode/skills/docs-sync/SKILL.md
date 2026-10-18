---
name: docs-sync
description: 同步 PCMannager 的文档与代码现状（AGENTS.md 维护规则、用例数、模块清单、ROADMAP 状态）。改动项目结构、构建/测试命令、开发约定、模块增删或完成 TODO 项后使用。
---

把文档同步到代码现状。AGENTS.md 的维护规则要求"结构/命令/约定变化必须同次改动同步"，但这类同步极易漏——已发生过真实漂移：README/TODO 曾写死用例数 175/202/210，全部失效。

## 第 0 步：先测量，禁止手抄数字

**任何可计算的量都必须现场跑命令取数，不得凭记忆或旧文档抄写。** 这是本 skill 存在的核心理由。

```bash
# 用例数（唯一可信来源；文档里禁止出现手写的用例数）
grep -rn "^func Test" --include=*_test.go internal/ modules/ | wc -l

# 模块清单
ls modules/

# 平台文件成对
git ls-files '*_windows.go' '*_other.go' | sed 's/_windows\.go$//;s/_other\.go$//' | sort | uniq -c

# 跟踪文件数 / Go 文件数
git ls-files | wc -l; git ls-files '*.go' | wc -l
```

## 第 1 步：定位漂移点

按改动类型确定要更新哪些文档：

| 改了什么 | 必须同步的文档 |
|---|---|
| 新增/删除模块、改模块 id | `AGENTS.md` 顶层结构、`README.md` 功能表、`docs/ROADMAP.md` |
| 构建/测试命令、CI 矩阵、产物命名 | `AGENTS.md` 构建段 + CI/发布段、`docs/DEVELOPMENT.md`、`.github/workflows/*.yml` |
| 架构边界、依赖方向、并发协议 | `AGENTS.md` 关键开发约定、`docs/MODULE-CONTRACT.md` |
| 完成 TODO/ROADMAP 项 | `TODO.md` 勾选项、`docs/ROADMAP.md` 状态、`CHANGELOG.md` |
| 新增"已知坑"或测试约定 | `AGENTS.md` 写测试段、`test-writer` skill |
| 新增/修改测试 | 只更新用例数（第 0 步命令取值），禁止手写 |

## 第 2 步：逐项更新

- 用 `edit_file` 精确替换，**不要整文件重写**——避免丢掉无关段落。
- 数字一律用第 0 步的输出；若文档里已有旧数字，替换而非叠加。
- 描述性文字（命令、路径、符号名）改完务必复核与实际代码一致。

## 第 3 步：交叉验证

- `AGENTS.md` 提到的每个 `modules/<name>/` 与 `internal/<pkg>/` 都真实存在。
- 文档里的构建/测试命令实际能跑通（`go build ./...`、`go vet ./...`）。
- 若同步过程中发现 AGENTS.md 自身有过时或矛盾的条款，一并修正并在汇报中指出。

## 第 4 步：汇报

列出改动了哪些文件、每个数字取值的来源命令、以及发现的既有漂移。没跑命令就填进文档的数字视为错误。
