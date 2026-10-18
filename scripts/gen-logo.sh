#!/usr/bin/env bash
# gen-logo.sh — 从 internal/logo 的几何重新生成文档用品牌 SVG。
#
# 方向是单向的：internal/logo（Go 几何）→ docs/site/assets/*.svg。程序运行时
# **不解析 SVG**（托盘图标由 logo.Render 直接光栅化成 HICON），这两份文件只是
# 同一个几何在 README / Wiki / Pages 上的镜像。改品牌形状或配色请改
# internal/logo/logo.go 后跑本脚本；不要手改 SVG——下次生成会被覆盖。
#
# 用法：
#   bash scripts/gen-logo.sh            # 重新生成（无变更则跳过写盘）
#   bash scripts/gen-logo.sh --check    # 只校验已提交文件是否与几何一致（CI 用）
set -euo pipefail

cd "$(dirname "$0")/.."

exec go run ./internal/logo/gen "$@"
