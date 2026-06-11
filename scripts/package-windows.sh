#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

CODEX_DESKTOP_PRODUCT_ID="9PLM9XGG6VKS"
CODEX_DESKTOP_ASSET="Codex Installer.exe"
CODEX_DESKTOP_URL="https://get.microsoft.com/installer/download/$CODEX_DESKTOP_PRODUCT_ID?cid=website_cta_psi"
CODEX_DESKTOP_CACHE="dist/cache/codex-desktop/$CODEX_DESKTOP_PRODUCT_ID/$CODEX_DESKTOP_ASSET"
LOOM_RELEASE="v0.0.3"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"
LOOM_DRIVER_ASSET="driver-agent.windows-amd64.exe"
LOOM_DRIVER_SHA256="502d356c37b63a9f17e7ab147000b7e1e6bfa0dd4893a50997c59c69ac5ad021"
LOOM_DRIVER_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_ASSET"
LOOM_SLAVE_ASSET="slave-agent.windows-amd64.exe"
LOOM_SLAVE_SHA256="965197e9a78ef61efb7d26da1bebe570fdf5e4f6743ca810c16f21fde369af46"
LOOM_SLAVE_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_SLAVE_ASSET"

verify_sha256() {
  local path expected sum
  path="$1"
  expected="$2"
  [[ -f "$path" ]] || return 1
  sum=$(sha256sum "$path" | awk '{print $1}')
  [[ "$sum" == "$expected" ]]
}

download_loom_asset() {
  local asset cache expected url sum
  asset="$1"
  cache="$2"
  expected="$3"
  url="$LOOM_BASE_URL/$asset"
  if verify_sha256 "$cache" "$expected"; then
    echo "$asset: $(stat -c%s "$cache") bytes (cached)"
    return 0
  fi
  mkdir -p "$(dirname "$cache")"
  rm -f "$cache" "$cache.part"
  echo "Fetching loom $asset ..."
  echo "  URL: $url"
  if ! curl --fail --location --retry 2 --retry-delay 2 --output "$cache.part" "$url"; then
    rm -f "$cache.part"
    echo "ERROR: failed to download loom $asset" >&2
    exit 2
  fi
  sum=$(sha256sum "$cache.part" | awk '{print $1}')
  if [[ "$sum" != "$expected" ]]; then
    rm -f "$cache.part"
    echo "ERROR: loom $asset SHA256 mismatch: got $sum want $expected" >&2
    exit 2
  fi
  mv "$cache.part" "$cache"
  echo "$asset: $(stat -c%s "$cache") bytes (cached)"
}

if [[ ! -f "$CODEX_DESKTOP_CACHE" ]]; then
  mkdir -p "$(dirname "$CODEX_DESKTOP_CACHE")"
  echo "Fetching Codex Desktop installer ..."
  echo "  URL: $CODEX_DESKTOP_URL"
  if ! curl --fail --location --retry 2 --retry-delay 2 --output "$CODEX_DESKTOP_CACHE.part" "$CODEX_DESKTOP_URL"; then
    rm -f "$CODEX_DESKTOP_CACHE.part"
    echo "ERROR: failed to download Codex Desktop installer" >&2
    exit 2
  fi
  mv "$CODEX_DESKTOP_CACHE.part" "$CODEX_DESKTOP_CACHE"
fi
codex_desktop_size=$(stat -c%s "$CODEX_DESKTOP_CACHE")
echo "Codex Desktop installer: $codex_desktop_size bytes (cached)"

download_loom_asset "$LOOM_DRIVER_ASSET" "$LOOM_DRIVER_CACHE" "$LOOM_DRIVER_SHA256"
download_loom_asset "$LOOM_SLAVE_ASSET" "$LOOM_SLAVE_CACHE" "$LOOM_SLAVE_SHA256"

# Pre-flight: cross-built binaries, .vsix, and bundled installer payloads must exist.
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         dist/windows/uninstall.exe dist/windows/token-refresher.exe \
         extensions/agentserver-app/agentserver-app-0.1.0.vsix \
         internal/ui/assets/dist/index.html \
         packaging/windows/install.ps1 \
         packaging/windows/ensure-vscode.ps1 \
         packaging/windows/ensure-codex.ps1 \
         packaging/windows/codex-manifest.json \
         packaging/windows/ensure-codex-desktop.ps1 \
         packaging/windows/write-install-mode.ps1 \
         packaging/windows/machine.ps1 \
         packaging/windows/ChineseSimplified.isl \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt \
         "$CODEX_DESKTOP_CACHE" \
         "$LOOM_DRIVER_CACHE" \
         "$LOOM_SLAVE_CACHE"; do
  if [[ ! -e "$f" ]]; then
    echo "missing: $f"
    case "$f" in
      internal/ui/assets/dist/*) echo "  hint: run 'make ui-build'" ;;
      dist/windows/*.exe)        echo "  hint: run 'make cross-windows'" ;;
      */agentserver-app-*.vsix) echo "  hint: run 'make ext-build'" ;;
    esac
    exit 1
  fi
done

# Find ISCC.exe (Inno Setup). Local install (Windows) or wine.
ISCC=()
if command -v ISCC.exe >/dev/null 2>&1; then
  ISCC=("ISCC.exe")
elif command -v iscc >/dev/null 2>&1; then
  ISCC=("iscc")
elif command -v wine >/dev/null 2>&1 && \
     [[ -f "$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe" ]]; then
  ISCC=("wine" "$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe")
else
  echo "Inno Setup not found. Install on Windows, or install via Wine:"
  echo "  wine innosetup-6.x.exe /VERYSILENT"
  exit 2
fi

cd packaging/windows
mkdir -p Output
"${ISCC[@]}" installer.iss
ls -la Output/
