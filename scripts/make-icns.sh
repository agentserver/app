#!/usr/bin/env bash
# 把 image/icon.png 转成 packaging/macos/icon.icns（需 macOS iconutil）。
set -euo pipefail
SRC="${1:-image/icon.png}"
OUT="packaging/macos/icon.icns"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ICONSET="$TMP/icon.iconset"
mkdir -p "$ICONSET"
for size in 16 32 64 128 256 512 1024; do
  half=$((size / 2))
  sips -z "$size" "$size" "$SRC" --out "$ICONSET/icon_${half}x${half}.png" >/dev/null
  sips -z "$half" "$half" "$SRC" --out "$ICONSET/icon_${half}x${half}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$OUT"
echo "wrote $OUT"
