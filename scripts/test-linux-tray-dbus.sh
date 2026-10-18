#!/usr/bin/env bash
# 在 Linux 容器里跑 Linux 托盘的真实 D-Bus 集成测试。
#
# 为什么需要这个脚本：tray_linux_dbus_test.go 只能在有 dbus-daemon 的 Linux 上跑，
# macOS/Windows 上会跳过。CI（ci.yml 的 tray-dbus job）会跑，但本地想复现时，
# 用这个脚本拉一个装了 dbus 的 golang 容器即可，无需本机装 Linux 或桌面环境。
#
# 用法：
#   bash scripts/test-linux-tray-dbus.sh
#
# 镜像源可按需覆盖（国内网络通常要把 DOCKER 镜像地址换成可达的镜像）：
#   PCM_GO_IMAGE=docker.m.daocloud.io/library/golang:1.27 bash scripts/test-linux-tray-dbus.sh
#
# 依赖：Docker。容器内 dbus 是为了让 dbus-daemon 可用；不代表 GNOME/KDE 托盘宿主，
# 但注册握手、属性、dbusmenu 与角标信号这些“协议层”行为都是真的总线往返。
set -euo pipefail

cd "$(dirname "$0")/.."

GO_IMAGE="${PCM_GO_IMAGE:-golang:1.27}"
RUN_ARGS=(
  --rm
  -v "$PWD":/src
  -v pcm-gomodcache:/go/pkg/mod
  -w /src
  -e GOTOOLCHAIN=go1.27.1
  -e GOPROXY=https://goproxy.cn,direct
  -e GOFLAGS=-mod=mod
  -e CGO_ENABLED=0
  # CI 同款：强制真实跑 D-Bus 用例，测试自己拉一个专用 dbus-daemon。
  -e PCM_REQUIRE_DBUS=1
)

if ! command -v docker >/dev/null 2>&1; then
  echo "未找到 docker，无法运行 Linux 托盘集成测试。" >&2
  exit 1
fi

echo "使用镜像: $GO_IMAGE"
docker run "${RUN_ARGS[@]}" "$GO_IMAGE" sh -c '
  set -e
  apt-get update -qq
  apt-get install -y -qq dbus
  echo "dbus-daemon: $(command -v dbus-daemon)"
  go test -count=1 -v -run TestLinuxTrayDBus ./internal/tray/
'
