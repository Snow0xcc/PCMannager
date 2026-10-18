#!/usr/bin/env bash
# build-env.sh — 构建参数的单一来源，由 build.sh 与 preflight.sh 共用。
#
# 抽出来是为了让"校验什么"和"构建什么"永远使用同一组标签：两处各写一份
# 必然漂移，而这里的分歧后果很重（漏 production 会产出启动即报错的包）。
#
# 用法（不执行，只 source）：
#   . scripts/build-env.sh
#
# 注意：本文件不设置 shell 选项，避免影响调用方的 set -euo pipefail。

# Wails 用构建标签选择实现。缺 production/dev 时 Wails 会选中
# internal/app/app_default_windows.go 的桩（CreateApp 弹错误框后返回 nil），
# 因此本项目固定走生产路径。
#
# desktop 标签是 Wails CLI 的 OutputType，但 v2.10.2 的源码里没有任何文件用它
# 做约束（实测 `go list -deps` 与仅 production 完全一致），故不必添加。
PCM_BUILD_TAGS="production"
