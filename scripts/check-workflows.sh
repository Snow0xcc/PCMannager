#!/usr/bin/env bash
# check-workflows.sh — 校验 .github/workflows/*.yml 的结构。
#
# 为什么需要：workflow 文件的语法错误不会在本地任何 Go 命令里暴露，只会在
# 推到 GitHub 之后整条流水线红掉（而且往往要等到发版才发现）。典型事故是把
# 多行 run 脚本误压成一行——YAML 仍能解析，但后续赋值会被 # 注释吃掉，运行时
# 才因 set -u 失败。
#
# 用法：scripts/check-workflows.sh
# 依赖：项目已有的 gopkg.in/yaml.v3（见 scripts/yamlcheck.go），无需额外安装。
set -euo pipefail

cd "$(dirname "$0")/.."

go run scripts/yamlcheck.go
