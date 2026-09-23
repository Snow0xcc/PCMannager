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

# 版本号：优先用调用方传入的 PCM_VERSION（CI 从 tag 推导），否则从 git
# describe 推导，最后兜底为 dev。未打 stamp 的构建会由 internal/app 从
# 内嵌 build info 回退，不会出现空白版本。
#
# Version 必须是 var 才能被 -X 注入；注入后 GET /api/state 与面板页眉显示
# 该版本，自动更新才有一个可信的比较基准。
version="${PCM_VERSION:-}"
if [ -z "$version" ]; then
  if git describe --tags --always --dirty >/dev/null 2>&1; then
    version=$(git describe --tags --always --dirty)
  else
    version="dev"
  fi
fi
ldflags="$ldflags -X github.com/snow0xcc/pcmannager/internal/app.Version=$version"
echo "version: $version"

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
