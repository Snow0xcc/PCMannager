#!/usr/bin/env bash
# build.sh — 按与 .github/workflows/release.yml 完全相同的参数构建单个目标。
#
# 单独抽出来是为了避免 CI 与本地构建参数漂移（尤其是 Windows 的 -H windowsgui，
# 漏掉它就会在启动时弹出黑色控制台窗口）。
#
# 用法：
#   scripts/build.sh <goos> <goarch> <输出文件>
#   scripts/build.sh windows amd64 dist/pcmannager-windows-amd64.exe
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "用法: $0 <goos> <goarch> <output>" >&2
  exit 2
fi

goos=$1
goarch=$2
out=$3

mkdir -p "$(dirname "$out")"

# -s -w 去掉符号表和调试信息以减小体积。
ldflags="-s -w"

# Windows 默认构建为 console 子系统，启动时会闪出黑色控制台窗口。
# -H windowsgui 切换到 GUI 子系统：没有控制台，日志只写文件与面板。
# 注意：这同时意味着 stdout/stderr 不可见，排查问题请看
# %APPDATA%\GoBox\gobox.log 或首选项面板的事件日志页。
if [ "$goos" = "windows" ]; then
  ldflags="$ldflags -H windowsgui"
fi

CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath -ldflags="$ldflags" -o "$out" .

echo "built: $out ($(wc -c <"$out") bytes)"
