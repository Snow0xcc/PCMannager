#!/usr/bin/env bash
# check-emoji.sh — 拒绝客户端界面中出现 emoji（项目硬约束，AGENTS.md）。
# 客户端界面一律用 icon 资源替代 emoji。
#
# 范围（ROADMAP A5）：
#   - *.html *.css *.js  前端资产，扫描 emoji 区段；
#     *.html 另加符号区段（箭头/几何符号等也不该进界面）。
#   - *.go               原生 UI（walk/winui 面板）的字符串字面量也在界面上，一并扫描。
#   - *.md 不在范围：文档排版使用 → / ✅ 等符号是合法的。
#
# 自检：脚本每次运行都会先用一个必须被命中的 fixture 验证探测器本身——
# 没有自检的检查器等于没有检查器。
# 用法：scripts/check-emoji.sh
set -euo pipefail

cd "$(dirname "$0")/.."

# emoji 区段（所有受扫文件）。必须用单引号 PCRE \x{...} 形式——
# bash $'\U...' 拼不出区间，旧实现的闸门因此从不失败。
emoji_pattern='[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{1F1E6}-\x{1F1FF}\x{1F3FB}-\x{1F3FF}]'
# 符号区段（仅 *.html：箭头、几何形状、变体选择符）。
symbol_pattern='[\x{2190}-\x{21FF}\x{2B00}-\x{2BFF}\x{FE00}-\x{FE0F}]'

fail=0

scan() { # scan <pattern> <file>
  if grep -Pq "$1" "$2" 2>/dev/null; then
    echo "EMOJI: $2"
    fail=1
  fi
}

# —— 自检：探测器必须能命中真违规，且不误报干净文件 ——
selftest_dir=$(mktemp -d)
trap 'rm -rf "$selftest_dir"' EXIT
printf 'bad \xF0\x9F\x9A\x80 emoji\n' > "$selftest_dir/bad.go"        # 🚀
printf 'clean arrow -> and check [ok]\n' > "$selftest_dir/good.go"
if ! grep -Pq "$emoji_pattern" "$selftest_dir/bad.go"; then
  echo "自检失败：探测器无法命中 emoji fixture，闸门无效（正则或 grep -P 环境问题）。"
  exit 1
fi
if grep -Pq "$emoji_pattern" "$selftest_dir/good.go"; then
  echo "自检失败：探测器误报了干净文件。"
  exit 1
fi

# —— 符号区段自检：html 专属的 symbol_pattern 写错同样会静默放行 ——
printf 'bad \xE2\x86\x92 arrow\n' > "$selftest_dir/bad.html"      # →（U+2192，仅落在符号区段）
if ! grep -Pq "$symbol_pattern" "$selftest_dir/bad.html"; then
  echo "自检失败：符号区段正则（symbol_pattern）无法命中 html 符号 fixture，闸门无效。"
  exit 1
fi
if grep -Pq "$symbol_pattern" "$selftest_dir/good.go"; then
  echo "自检失败：符号区段正则（symbol_pattern）误报了干净文件 good.go。"
  exit 1
fi

# —— 扫描（仅 git 跟踪的文件；reference/ 是 submodule，不产出独立文件条目） ——
while IFS= read -r f; do
  case "$f" in
    reference/*) continue ;;
  esac
  scan "$emoji_pattern" "$f"
done < <(git ls-files -- '*.html' '*.css' '*.js' '*.go' 2>/dev/null || true)

while IFS= read -r f; do
  case "$f" in
    reference/*) continue ;;
  esac
  scan "$symbol_pattern" "$f"
done < <(git ls-files -- '*.html' 2>/dev/null || true)

if [ "$fail" -ne 0 ]; then
  echo "发现 emoji，违反前端禁用 emoji 约束，请改用 icon 资源。"
  exit 1
fi
echo "emoji 检查通过（含探测器自检）：未发现 emoji。"
