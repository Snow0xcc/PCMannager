#!/usr/bin/env bash
# preflight.sh — 构建前必须通过的基线检查。
#
# 为什么需要它：这个项目有两类"构建成功但运行即坏"的坑，编译器不会报错。
#   1. 缺 Wails 的 production 标签 → 链接到 Wails 的报错桩，启动弹英文错误框；
#   2. Windows 缺 -H windowsgui → 启动弹出黑色控制台窗口。
# 两者都只靠"记得用对命令"约束，而 IDE 的运行按钮、`go run .`、手写
# `go build` 都会绕过它。本脚本把"构建前必须满足什么"变成可执行的断言。
#
# 用法：
#   scripts/preflight.sh [goos] [goarch]      # 默认取当前平台
#   scripts/preflight.sh windows amd64 --with-tests
#
# 退出码：0 通过，1 基线不满足，2 用法错误。
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=build-env.sh
. ./scripts/build-env.sh

usage() {
  echo "用法: $0 [goos] [goarch] [--with-tests]" >&2
  exit 2
}

with_tests=0
positional=()
for arg in "$@"; do
  case "$arg" in
    --with-tests) with_tests=1 ;;
    -h|--help) usage ;;
    -*) echo "未知参数: $arg" >&2; usage ;;
    *) positional+=("$arg") ;;
  esac
done

if [ "${#positional[@]}" -gt 2 ]; then
  usage
fi

goos="${positional[0]:-$(go env GOOS)}"
goarch="${positional[1]:-$(go env GOARCH)}"

fail=0
step() { printf '\n==> %s\n' "$1"; }
ok() { echo "OK   $1"; }
bad() {
  echo "FAIL $1" >&2
  fail=1
}

# ---------------------------------------------------------------------------
# 1. Go 工具链与 go.mod 要求的最低版本
# ---------------------------------------------------------------------------
step "Go 工具链"
if ! command -v go >/dev/null 2>&1; then
  bad "未找到 go，请先安装 Go 并加入 PATH"
  exit 1
fi

go_version="$(go env GOVERSION)"
want="$(sed -n 's/^go[[:space:]]\+\([0-9][0-9.]*\).*/\1/p' go.mod | head -n 1)"
if [ -z "$want" ]; then
  bad "无法从 go.mod 解析 go 指令"
else
  # 逐段比较数字，避免 "1.9" > "1.27" 这类字符串误判。
  if ! awk -v have="${go_version#go}" -v need="$want" 'BEGIN {
        n = split(have, h, "."); m = split(need, r, ".");
        len = (n > m) ? n : m;
        for (i = 1; i <= len; i++) {
          hv = (i <= n) ? h[i] + 0 : 0;
          rv = (i <= m) ? r[i] + 0 : 0;
          if (hv > rv) exit 0;
          if (hv < rv) exit 1;
        }
        exit 0;
      }'; then
    bad "Go 版本过低：当前 ${go_version}，go.mod 要求 >= ${want}"
  else
    ok "Go ${go_version} 满足 go.mod 要求 >= ${want}"
  fi
fi

# ---------------------------------------------------------------------------
# 2. 构建标签契约（本项目最贵的一类故障）
# ---------------------------------------------------------------------------
step "构建标签契约"

case " $PCM_BUILD_TAGS " in
  *" production "*) ok "PCM_BUILD_TAGS 含 production（${PCM_BUILD_TAGS}）" ;;
  *) bad "PCM_BUILD_TAGS 缺少 production（当前：${PCM_BUILD_TAGS:-空}）" ;;
esac

# 仅 Windows 会链接 Wails：断言所选实现不是报错桩。这条检查比"记得别漏标签"
# 更硬——即使将来有人改了标签名，它也会当场失败，而不是产出弹框的包。
if [ "$goos" = "windows" ]; then
  files="$(GOOS="$goos" GOARCH="$goarch" go list -tags "$PCM_BUILD_TAGS" \
    -f '{{join .GoFiles " "}}' github.com/wailsapp/wails/v2/internal/app 2>/dev/null || true)"
  if [ -z "$files" ]; then
    bad "无法列出 Wails 内部 app 包文件（依赖是否完整？先跑 go mod download）"
  else
    case " $files " in
      *" app_default_windows.go "*) bad "构建将链接 Wails 报错桩 app_default_windows.go（标签未生效）" ;;
      *) ok "Wails 未选中报错桩" ;;
    esac
    case " $files " in
      *" app_production.go "*) ok "Wails 选中生产实现 app_production.go" ;;
      *) bad "构建未选中 Wails 生产实现 app_production.go" ;;
    esac
  fi
else
  ok "非 Windows 目标不链接 Wails，跳过标签实现检查"
fi

# ---------------------------------------------------------------------------
# 3. 依赖一致性
# ---------------------------------------------------------------------------
step "依赖一致性"
if go mod verify >/dev/null 2>&1; then
  ok "go.sum 校验通过"
else
  bad "go.sum 校验失败，请检查依赖是否被篡改"
fi

# go mod tidy -diff 需要 Go 1.23+；不认识该参数时降级为警告而不是误报失败。
if tidy_out="$(go mod tidy -diff 2>&1)"; then
  ok "go.mod/go.sum 已 tidy"
elif echo "$tidy_out" | grep -qi "flag provided but not defined"; then
  echo "SKIP 当前 Go 不支持 go mod tidy -diff，跳过 tidy 检查"
elif [ -n "$tidy_out" ]; then
  bad "go.mod/go.sum 未 tidy（运行 go mod tidy）："
  echo "$tidy_out" >&2
else
  ok "go.mod/go.sum 已 tidy"
fi

# ---------------------------------------------------------------------------
# 4. 格式化
# ---------------------------------------------------------------------------
step "gofmt"
# 注意：本机 core.autocrlf=true 时工作区是 CRLF，而 gofmt 只输出 LF，
# 直接 `gofmt -l .` 会把每个文件都判为未格式化（CI 在 Linux 检出则正常）。
# 因此先抹掉 CR 再比较，只对真实的格式问题报错。
unformatted=""
checked=0
# 优先用 git 列出受版本控制的文件；没有 git（归档包/容器）时回退到 find，
# 避免“0 个文件”被当成通过。
list_go_files() {
  if git rev-parse --git-dir >/dev/null 2>&1; then
    git ls-files '*.go' | grep -v '^reference/' || true
  else
    find . -name '*.go' -not -path './reference/*' -not -path './.git/*' | sed 's|^\./||' || true
  fi
}
while IFS= read -r f; do
  [ -n "$f" ] || continue
  [ -f "$f" ] || continue
  checked=$((checked + 1))
  if ! gofmt "$f" | diff -q - <(tr -d '\r' <"$f") >/dev/null 2>&1; then
    unformatted="${unformatted}${f}"$'\n'
  fi
done < <(list_go_files)

if [ -n "$unformatted" ]; then
  bad "以下文件未通过 gofmt（运行 gofmt -w 修复）："
  printf '%s' "$unformatted" >&2
elif [ "$checked" -eq 0 ]; then
  bad "未找到任何 .go 文件，gofmt 检查无法进行（工作目录是否正确？）"
else
  ok "gofmt 通过（${checked} 个文件）"
fi

# ---------------------------------------------------------------------------
# 5. CI 工作流结构
# ---------------------------------------------------------------------------
step "CI 工作流"
if bash scripts/check-workflows.sh; then
  ok "workflow YAML 结构合法"
else
  bad "workflow YAML 不合法（GitHub 会直接拒绝该流水线）"
fi

# ---------------------------------------------------------------------------
# 6. go vet（按目标平台）
# ---------------------------------------------------------------------------
step "go vet（${goos}/${goarch}）"
if GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go vet ./... >/tmp/pcm-vet.$$ 2>&1; then
  ok "go vet 通过"
else
  bad "go vet 失败："
  cat /tmp/pcm-vet.$$ >&2
fi
rm -f /tmp/pcm-vet.$$

# ---------------------------------------------------------------------------
# 7. 单测（可选：默认不跑，CI 有独立 test job）
# ---------------------------------------------------------------------------
if [ "$with_tests" -eq 1 ]; then
  # 目标平台与宿主相同时才真正执行测试；跨平台时只能编译测试二进制
  # （直接运行会 exec format error，这一点在 Linux 上验 Windows 时踩过）。
  host_goos="$(go env GOOS)"
  host_goarch="$(go env GOARCH)"
  if [ "$goos" = "$host_goos" ] && [ "$goarch" = "$host_goarch" ]; then
    step "go test"
    if GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go test -count=1 ./internal/... ./modules/... >/tmp/pcm-test.$$ 2>&1; then
      ok "单测通过"
    else
      bad "单测失败："
      tail -n 40 /tmp/pcm-test.$$ >&2
    fi
    rm -f /tmp/pcm-test.$$
  else
    step "go test -c（${goos}/${goarch}，仅编译不执行）"
    tdir="$(mktemp -d)"
    if ! GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go list ./internal/... ./modules/... >/tmp/pcm-pkgs.$$ 2>&1; then
      bad "go list 失败："
      cat /tmp/pcm-pkgs.$$ >&2
    else
      compiled=0
      while IFS= read -r pkg; do
        [ -n "$pkg" ] || continue
        dir="$(go list -f '{{.Dir}}' "$pkg" 2>/dev/null || true)"
        # 无 _test.go 的包没有可编译的测试二进制，直接跳过。
        if [ -z "$dir" ] || ! compgen -G "$dir/*_test.go" >/dev/null; then
          continue
        fi
        if GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go test -c -o "$tdir" "$pkg" >/tmp/pcm-testc.$$ 2>&1; then
          compiled=$((compiled + 1))
        else
          bad "测试二进制编译失败：${pkg}"
          tail -n 40 /tmp/pcm-testc.$$ >&2
        fi
      done </tmp/pcm-pkgs.$$
      if [ "$compiled" -eq 0 ]; then
        bad "没有编译任何测试包，跨平台测试闸门失效"
      else
        ok "已编译 ${compiled} 个测试包（未执行）"
      fi
    fi
    rm -f /tmp/pcm-pkgs.$$ /tmp/pcm-testc.$$
    rm -rf "$tdir"
  fi
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "构建基线未满足，已中止构建。" >&2
  exit 1
fi
echo "构建基线全部通过（${goos}/${goarch}）。"
