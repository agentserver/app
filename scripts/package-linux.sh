#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.1.8}"
OUT="${OUT:-dist}"
DRY_RUN="${DRY_RUN:-0}"
source scripts/linux-package-common.sh

mkdir -p "$OUT/cache/loom/$LOOM_RELEASE" "$OUT/linux"

loom_cache_path() {
  local asset
  asset="$1"
  echo "$OUT/cache/loom/$LOOM_RELEASE/$asset"
}

preflight_binaries() {
  local arch
  for arch in amd64 arm64; do
    if [[ ! -x "$OUT/linux/$arch/agentserver" ]]; then
      echo "ERROR: missing prebuilt $OUT/linux/$arch/agentserver; run make cross-linux first" >&2
      exit 2
    fi
  done
}

download_support_assets() {
  download_loom_asset \
    "$LOOM_DRIVER_SKILLS_ASSET" \
    "$(loom_cache_path "$LOOM_DRIVER_SKILLS_ASSET")" \
    "$LOOM_DRIVER_SKILLS_SHA256"
  python3 scripts/package-superpower-skills.py "$OUT/cache/superpowers/$SUPERPOWER_SKILLS_ASSET"
  python3 scripts/package-driver-codex-prompts.py "$(loom_cache_path "$LOOM_DRIVER_CODEX_PROMPTS_ASSET")"
}

download_arch_assets() {
  local arch driver_asset driver_sha slave_asset slave_sha
  arch="$1"
  read -r driver_asset driver_sha < <(loom_asset_for_arch driver "$arch")
  read -r slave_asset slave_sha < <(loom_asset_for_arch slave "$arch")
  download_loom_asset "$driver_asset" "$(loom_cache_path "$driver_asset")" "$driver_sha"
  download_loom_asset "$slave_asset" "$(loom_cache_path "$slave_asset")" "$slave_sha"
}

stage_arch() {
 local arch stage tarball driver_asset driver_sha slave_asset slave_sha
  arch="$1"
  stage="$OUT/linux/agentserver-linux-$arch"
  tarball="$OUT/linux/agentserver-linux-$arch.tar.gz"
  read -r driver_asset driver_sha < <(loom_asset_for_arch driver "$arch")
  read -r slave_asset slave_sha < <(loom_asset_for_arch slave "$arch")

  if [[ "$DRY_RUN" == "1" ]]; then
    echo "dry-run: would stage $stage"
    echo "dry-run: agentserver <- $OUT/linux/$arch/agentserver"
    echo "dry-run: driver-agent <- $(loom_cache_path "$driver_asset")"
    echo "dry-run: slave-agent <- $(loom_cache_path "$slave_asset")"
    echo "dry-run: manifest <- packaging/linux/codex-manifest-linux-$arch.json"
    echo "dry-run: would write $tarball"
    return 0
  fi

  rm -rf "$stage"
  mkdir -p "$stage"
  cp "$OUT/linux/$arch/agentserver" "$stage/agentserver"
  cp "$(loom_cache_path "$driver_asset")" "$stage/driver-agent"
  cp "$(loom_cache_path "$slave_asset")" "$stage/slave-agent"
  cp "$(loom_cache_path "$LOOM_DRIVER_SKILLS_ASSET")" "$stage/driver-skills.tar.gz"
  cp "$OUT/cache/superpowers/$SUPERPOWER_SKILLS_ASSET" "$stage/driver-superpower-skills.tar.gz"
  cp "$(loom_cache_path "$LOOM_DRIVER_CODEX_PROMPTS_ASSET")" "$stage/driver-codex-prompts.tar.gz"
  cp "packaging/linux/codex-manifest-linux-$arch.json" "$stage/codex-manifest-linux-$arch.json"
  chmod 0755 "$stage/agentserver" "$stage/driver-agent" "$stage/slave-agent"

  rm -f "$tarball" "$tarball.sha256"
  (cd "$OUT/linux" && tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@0 -czf "agentserver-linux-$arch.tar.gz" "agentserver-linux-$arch")
  (cd "$OUT/linux" && sha256sum "agentserver-linux-$arch.tar.gz" > "agentserver-linux-$arch.tar.gz.sha256")
  echo "wrote $tarball"
}

preflight_binaries
download_support_assets
for arch in amd64 arm64; do
  download_arch_assets "$arch"
  stage_arch "$arch"
done
