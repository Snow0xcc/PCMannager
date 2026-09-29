#!/usr/bin/env bash
# sync-wiki.sh — 把 docs/wiki/ 同步到 GitHub Wiki 仓库。
#
# Wiki 是一个独立的 git 仓库（<owner>/<repo>.wiki.git），本脚本：
#   1. 克隆（或初始化）wiki 仓库到临时目录；
#   2. 用 docs/wiki/*.md 覆盖其中的同名页面；
#   3. 把 Home.md 里的 {TAG} / {REPO} 占位符替换为实际值；
#   4. 提交并推送（无变更则跳过）。
#
# 为什么用仓库内文件而不是 CI 里的 heredoc：heredoc 让文档脱离版本控制、
# 无法 review，且缩进/转义极易出错（前版正是因 heredoc 前导空白需要 sed
# 修正）。单一数据源 + 渲染脚本，文档随代码一起 review 与演进。
#
# 用法：
#   TAG=v1.2.3 REPO=owner/repo GITHUB_TOKEN=xxx bash scripts/sync-wiki.sh
set -euo pipefail

cd "$(dirname "$0")/.."

TAG="${TAG:?需要 TAG 环境变量（如 v1.2.3）}"
REPO="${REPO:?需要 REPO 环境变量（如 owner/repo）}"
TOKEN="${GITHUB_TOKEN:?需要 GITHUB_TOKEN 环境变量}"

SRC="docs/wiki"
if [ ! -d "$SRC" ]; then
  echo "::error::缺少文档源目录 $SRC"
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

WIKI_URL="https://x-access-token:${TOKEN}@github.com/${REPO}.wiki.git"

# Wiki 未开启时克隆会失败；降级为本地初始化，让首次推送能建库。
if ! git clone --quiet "$WIKI_URL" "$WORK/wiki" 2>/dev/null; then
  echo "::notice::Wiki 尚未初始化，本地 init 后推送"
  mkdir -p "$WORK/wiki"
  git -C "$WORK/wiki" init --initial-branch=master --quiet
  git -C "$WORK/wiki" remote add origin "$WIKI_URL"
fi

cd "$WORK/wiki"
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

# 覆盖所有页面；占位符在此渲染（源码里保留 {TAG}/{REPO} 以便本地预览）。
changed=0
for f in "$OLDPWD"/$SRC/*.md; do
  [ -f "$f" ] || continue
  name="$(basename "$f")"
  sed -e "s|{TAG}|${TAG}|g" -e "s|{REPO}|${REPO}|g" "$f" > "$WORK/wiki/$name"
  git add -- "$name"
  if ! git diff --cached --quiet -- "$name"; then
    changed=1
  fi
done

if [ "$changed" -eq 0 ]; then
  echo "Wiki 无变更，跳过提交"
  exit 0
fi

git commit --quiet -m "docs(wiki): update for ${TAG}"
# Wiki 默认分支历史上是 master，新仓库可能是 main；逐级回退。
git push --quiet origin master 2>/dev/null \
  || git push --quiet origin main 2>/dev/null \
  || git push --quiet origin HEAD:refs/heads/master
echo "Wiki 已同步（${TAG}）"
