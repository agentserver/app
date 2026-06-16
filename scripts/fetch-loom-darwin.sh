#!/usr/bin/env bash
# Download Loom driver-agent / slave-agent darwin binaries, lipo them into
# universal outputs in dist/macos/bin/. Verifies each download against the
# release's sha256sums.txt (authoritative).
#
# NOTE: Loom v0.0.5 does NOT publish darwin builds. When a darwin asset is
# absent from sha256sums.txt this script skips it (does not fail), so the .app
# can still be built; local-slave features stay unavailable on macOS until a
# Loom darwin release exists. See packaging/macos/MAC_HANDOFF.md.
set -euo pipefail
LOOM_VER="${LOOM_VER:-v0.0.5}"
BASE="${LOOM_BASE_URL:-https://github.com/agentserver/loom/releases/download}"
OUT="dist/macos/bin"
CACHE="dist/cache/loom/$LOOM_VER"
mkdir -p "$OUT" "$CACHE"

SUMS="$CACHE/sha256sums.txt"
if [[ ! -s "$SUMS" ]]; then
  curl -fsSL --retry 2 --output "$SUMS" "$BASE/$LOOM_VER/sha256sums.txt"
fi

# expected_sha <asset> -> echo sha256, or empty if not in sums.
expected_sha() {
  awk -v a="$1" '$2==a {print $1; found=1} END{exit !found}' "$SUMS" 2>/dev/null || true
}

verify_sha() { # <path> <expected> -> 0 if match
  local sum
  sum=$(sha256sum "$1" | awk '{print $1}')
  [[ "$sum" == "$2" ]]
}

have_binary=0
for kind in driver-agent slave-agent; do
  present=()
  for arch in arm64 amd64; do
    asset="${kind}.darwin-${arch}"
    want="$(expected_sha "$asset")"
    if [[ -z "$want" ]]; then
      echo "loom $asset: not published in $LOOM_VER (skipping)" >&2
      continue
    fi
    cache="$CACHE/$asset"
    if ! verify_sha "$cache" "$want"; then
      echo "Fetching loom $asset ..."
      curl -fsSL --retry 2 --output "$cache.part" "$BASE/$LOOM_VER/$asset"
      sum=$(sha256sum "$cache.part" | awk '{print $1}')
      if [[ "$sum" != "$want" ]]; then
        echo "ERROR: $asset SHA256 mismatch: got $sum want $want" >&2
        rm -f "$cache.part"
        exit 2
      fi
      mv "$cache.part" "$cache"
    fi
    present+=("$arch:$cache")
  done

  if [[ ${#present[@]} -eq 0 ]]; then
    echo "loom $kind: no darwin build in $LOOM_VER — output omitted" >&2
    continue
  fi
  have_binary=1
  if [[ ${#present[@]} -eq 1 ]]; then
    cp "${present[0]#*:}" "$OUT/$kind"
    echo "loom $kind: single-arch (${present[0]%%:*}) — not universal"
  else
    lipo -create -output "$OUT/$kind" "${present[0]#*:}" "${present[1]#*:}"
    echo "loom $kind: universal (arm64+amd64)"
  fi
done

if [[ $have_binary -eq 0 ]]; then
  echo "WARNING: Loom $LOOM_VER has no darwin driver-agent/slave-agent. The .app builds without them; local-slave features are unavailable on macOS until a Loom darwin release exists." >&2
fi
