#!/usr/bin/env bash
# 下载 Loom v0.0.5 的 darwin driver-agent / slave-agent（arm64+amd64），lipo 成 universal，
# 输出到 dist/macos/bin/。SHA256 从 Loom release assets 元数据取（与 linux 流程镜像）。
set -euo pipefail
LOOM_VER="v0.0.5"
BASE="${LOOM_BASE_URL:-https://github.com/agentserver/loom/releases/download}"
OUT="dist/macos/bin"
mkdir -p "$OUT"
for kind in driver-agent slave-agent; do
  for arch in arm64 amd64; do
    url="$BASE/$LOOM_VER/${kind}.darwin-${arch}"
    cache="dist/cache/loom/$LOOM_VER/${kind}.darwin-${arch}"
    [[ -f "$cache" ]] || curl -fL --retry 3 -o "$cache" "$url"
  done
  lipo -create -output "$OUT/$kind" \
    "dist/cache/loom/$LOOM_VER/${kind}.darwin-arm64" \
    "dist/cache/loom/$LOOM_VER/${kind}.darwin-amd64"
done
