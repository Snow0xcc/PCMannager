#!/usr/bin/env bash
# check-emoji.sh — 拒绝客户端界面中出现 emoji（项目硬约束）。
# 客户端界面一律用 icon 资源替代 emoji，正则覆盖常见 emoji 区段。
# 用法：scripts/check-emoji.sh
set -euo pipefail

cd "$(dirname "$0")/.."

pattern=$'\U0001F000-\U0001FAFF\U00002600-\U000027BF\U0001F1E6-\U0001F1FF\U00002190-\U000021FF\U00002B00-\U00002BFF\U0000FE00-\U0000FE0F\U0001F3FB-\U0001F3FF'

fail=0
while IFS= read -r f; do
  # 仅扫描前端与文档；参考仓库为第三方，不在范围。
  case "$f" in
    ./reference/*|./.git/*) continue ;;
  esac
  if grep -Pl "$pattern" "$f" >/dev/null 2>&1; then
    echo "EMOJI: $f"
    fail=1
  fi
done < <(git ls-files -- '*.html' '*.css' '*.js' '*.md' '*.go' 2>/dev/null || true)

if [ "$fail" -ne 0 ]; then
  echo "发现 emoji，违反前端禁用 emoji 约束，请改用 icon 资源。"
  exit 1
fi
echo "emoji 检查通过：未发现 emoji。"
