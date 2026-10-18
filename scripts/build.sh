#!/usr/bin/env bash
# build.sh — 按与 .github/workflows/release.yml 完全相同的参数构建单个目标。
#
# 单独抽出来是为了避免 CI 与本地构建参数漂移（尤其是 Windows 的 -H windowsgui，
# 漏掉它就会在启动时弹出黑色控制台窗口）。
#
# 用法：
#   scripts/build.sh <goos> <goarch> <输出文件> [--skip-preflight]
#   scripts/build.sh windows amd64 dist/pcmannager-windows-amd64.exe
#
# 默认先跑 scripts/preflight.sh（构建前基线），任一基线不满足就中止构建；
# CI 中已由独立的 preflight job 覆盖时可加 --skip-preflight 避免重复检查。
# 构建完成后仍会对产物做校验（标签与子系统），这是最后一道防线。
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=build-env.sh
. ./scripts/build-env.sh

if [ "$#" -lt 3 ]; then
  echo "用法: $0 <goos> <goarch> <output> [--skip-preflight]" >&2
  exit 2
fi

goos=$1
goarch=$2
out=$3
shift 3

skip_preflight=0
for arg in "$@"; do
  case "$arg" in
    --skip-preflight) skip_preflight=1 ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

mkdir -p "$(dirname "$out")"

if [ "$skip_preflight" -eq 0 ]; then
  bash scripts/preflight.sh "$goos" "$goarch"
fi

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
  go build -tags "$PCM_BUILD_TAGS" -trimpath -ldflags="$ldflags" -o "$out" .

# ---------------------------------------------------------------------------
# 产物校验：以上参数都正确时这两条必然成立；任何一条失败都说明构建参数被
# 改写（或被 go env 里的 GOFLAGS 覆盖），此时宁可失败也不发布坏包。
# ---------------------------------------------------------------------------
verify_fail=0

if ! go version -m "$out" 2>/dev/null | grep -Eq -- '-tags=[^[:space:]]*production'; then
  echo "FAIL 产物未带 production 构建标签（Wails 会退回报错桩）" >&2
  verify_fail=1
fi

if [ "$goos" = "windows" ]; then
  # Wails 的报错桩内嵌了这句英文；它出现在产物里就证明链接到了桩。
  if grep -aq "will not build without the correct build tags" "$out"; then
    echo "FAIL 产物包含 Wails 报错桩文案，启动会弹出错误框" >&2
    verify_fail=1
  fi

  # PE 可选头 Subsystem 字段：2 = GUI，3 = 控制台。
  # 偏移固定：e_lfanew 在 DOS 头 +0x3C，PE 签名 4 字节 + 文件头 20 字节后
  # 是可选头，Subsystem 位于可选头 +68（PE32/PE32+ 一致）。
  e_lfanew=$(od -An -tu4 -j60 -N4 "$out" | tr -d ' \n')
  subsystem=$(od -An -tu2 -j$((e_lfanew + 92)) -N2 "$out" | tr -d ' \n')
  if [ "$subsystem" != "2" ]; then
    echo "FAIL 产物子系统为 ${subsystem:-未知}（应为 2=GUI），启动会弹出控制台窗口" >&2
    verify_fail=1
  fi
fi

if [ "$verify_fail" -ne 0 ]; then
  echo "产物校验失败，已删除: $out" >&2
  rm -f "$out"
  exit 1
fi

echo "built: $out ($(wc -c <"$out") bytes)"
