#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

CODEX_RELEASE="rust-v0.136.0"
CODEX_ASSET="codex-x86_64-pc-windows-msvc.exe"
CODEX_URL="https://github.com/openai/codex/releases/download/$CODEX_RELEASE/$CODEX_ASSET"
CODEX_CACHE="dist/cache/$CODEX_RELEASE/$CODEX_ASSET"
CODEX_DESKTOP_PRODUCT_ID="9PLM9XGG6VKS"
CODEX_DESKTOP_ASSET="Codex Installer.exe"
CODEX_DESKTOP_URL="https://get.microsoft.com/installer/download/$CODEX_DESKTOP_PRODUCT_ID?cid=website_cta_psi"
CODEX_DESKTOP_CACHE="dist/cache/codex-desktop/$CODEX_DESKTOP_PRODUCT_ID/$CODEX_DESKTOP_ASSET"
LOOM_RELEASE="v0.0.4"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"
LOOM_DRIVER_ASSET="driver-agent.windows-amd64.exe"
LOOM_DRIVER_SHA256="f2f3d3ed2e27f9d681640b4884bdc78d807cef4ba2f9b9afaa19ccbcffe5796e"
LOOM_DRIVER_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_ASSET"
LOOM_SLAVE_ASSET="slave-agent.windows-amd64.exe"
LOOM_SLAVE_SHA256="92e39b6e38c198a997ecb7d5102232934d578a66a00265ee5a0981e13bd7a97d"
LOOM_SLAVE_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_SLAVE_ASSET"
LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="7086dd93f3181c552fbe475c4698aa809c746ecd48dc5ed942539377116ed9cc"
LOOM_DRIVER_SKILLS_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_SKILLS_ASSET"
SUPERPOWER_SKILLS_CACHE="dist/cache/superpowers/driver-superpower-skills.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_ASSET="driver-codex-prompts.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_SHA256="dbbc4cc87cf2cfb377f7ec188610839ff9152ec05fa07adde999fdb39f2d6721"
LOOM_DRIVER_CODEX_PROMPTS_CACHE="dist/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_CODEX_PROMPTS_ASSET"
VSCODE_MANIFEST="packaging/windows/vscode-manifest.json"

eval "$(
python3 - "$VSCODE_MANIFEST" <<'PYEOF'
import json, shlex, sys
with open(sys.argv[1], encoding='utf-8') as f:
    m = json.load(f)
print('VSCODE_VERSION=' + shlex.quote(m['version']))
print('VSCODE_SHA256=' + shlex.quote(m['sha256']))
print('VSCODE_SIZE=' + shlex.quote(str(m['expected_size'])))
print('VSCODE_URL=' + shlex.quote(m['urls'][0]))
PYEOF
)"
VSCODE_CACHE="dist/cache/vscode/$VSCODE_VERSION/VSCodeUserSetup-x64-$VSCODE_VERSION.exe"
mapfile -t VSCODE_URLS < <(
python3 - "$VSCODE_MANIFEST" <<'PYEOF'
import json, sys
with open(sys.argv[1], encoding='utf-8') as f:
    m = json.load(f)
for url in m.get('urls', []):
    print(url)
PYEOF
)
if [[ "${#VSCODE_URLS[@]}" -eq 0 ]]; then
  echo "ERROR: no VS Code installer URLs in $VSCODE_MANIFEST" >&2
  exit 2
fi

verify_vscode_cache() {
  [[ -f "$VSCODE_CACHE" ]] || return 1
  local size
  size=$(stat -c%s "$VSCODE_CACHE")
  [[ "$size" == "$VSCODE_SIZE" ]] || return 1
  local sum
  sum=$(sha256sum "$VSCODE_CACHE" | awk '{print $1}')
  [[ "$sum" == "$VSCODE_SHA256" ]]
}

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

download_vscode_installer() {
  local attempt max_attempts local_size url
  max_attempts=2
  for url in "${VSCODE_URLS[@]}"; do
    echo "  URL: $url"
    for ((attempt = 1; attempt <= max_attempts; attempt++)); do
      if curl --fail --location --continue-at - --retry 2 --retry-delay 2 --retry-connrefused \
        --speed-limit 131072 --speed-time 30 \
        --output "$VSCODE_CACHE.part" "$url"; then
        local_size=$(stat -c%s "$VSCODE_CACHE.part")
        if [[ "$local_size" == "$VSCODE_SIZE" ]]; then
          return 0
        fi
        if (( local_size > VSCODE_SIZE )); then
          rm -f "$VSCODE_CACHE.part"
        fi
        echo "VS Code installer partial size $local_size/$VSCODE_SIZE; retrying..." >&2
      else
        echo "VS Code installer download attempt $attempt/$max_attempts failed from $url; retrying..." >&2
      fi
      sleep 2
    done
  done
  return 1
}

if [[ ! -f "$CODEX_CACHE" ]]; then
  mkdir -p "$(dirname "$CODEX_CACHE")"
  echo "Fetching codex.exe (246MB, one-time) ..."
  echo "  URL: $CODEX_URL"
  if ! curl --fail --location --progress-bar --output "$CODEX_CACHE.part" "$CODEX_URL"; then
    rm -f "$CODEX_CACHE.part"
    echo "ERROR: failed to download codex.exe" >&2
    echo "If you're in China and direct GitHub is blocked, try:" >&2
    echo "  curl -fL -o $CODEX_CACHE 'https://gh-proxy.com/$CODEX_URL'" >&2
    exit 2
  fi
  mv "$CODEX_CACHE.part" "$CODEX_CACHE"
fi
codex_size=$(stat -c%s "$CODEX_CACHE")
echo "codex.exe: $codex_size bytes (cached)"

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
download_loom_asset "$LOOM_DRIVER_SKILLS_ASSET" "$LOOM_DRIVER_SKILLS_CACHE" "$LOOM_DRIVER_SKILLS_SHA256"
download_loom_asset "$LOOM_DRIVER_CODEX_PROMPTS_ASSET" "$LOOM_DRIVER_CODEX_PROMPTS_CACHE" "$LOOM_DRIVER_CODEX_PROMPTS_SHA256"
python3 scripts/package-superpower-skills.py "$SUPERPOWER_SKILLS_CACHE"

if ! verify_vscode_cache; then
  mkdir -p "$(dirname "$VSCODE_CACHE")"
  rm -f "$VSCODE_CACHE"
  if [[ -f "$VSCODE_CACHE.part" ]]; then
    part_size=$(stat -c%s "$VSCODE_CACHE.part")
    if (( part_size > VSCODE_SIZE )); then
      rm -f "$VSCODE_CACHE.part"
    fi
  fi
  echo "Fetching VS Code installer $VSCODE_VERSION (100MB, one-time) ..."
  if ! download_vscode_installer; then
    local_size=0
    [[ -f "$VSCODE_CACHE.part" ]] && local_size=$(stat -c%s "$VSCODE_CACHE.part")
    echo "ERROR: VS Code installer download incomplete: got $local_size want $VSCODE_SIZE" >&2
    exit 2
  fi
  local_size=$(stat -c%s "$VSCODE_CACHE.part")
  if [[ "$local_size" != "$VSCODE_SIZE" ]]; then
    rm -f "$VSCODE_CACHE.part"
    echo "ERROR: VS Code installer size mismatch: got $local_size want $VSCODE_SIZE" >&2
    exit 2
  fi
  local_sum=$(sha256sum "$VSCODE_CACHE.part" | awk '{print $1}')
  if [[ "$local_sum" != "$VSCODE_SHA256" ]]; then
    rm -f "$VSCODE_CACHE.part"
    echo "ERROR: VS Code installer SHA256 mismatch: got $local_sum want $VSCODE_SHA256" >&2
    exit 2
  fi
  mv "$VSCODE_CACHE.part" "$VSCODE_CACHE"
fi
vscode_size=$(stat -c%s "$VSCODE_CACHE")
echo "vscode installer: $vscode_size bytes (cached)"

# Pre-flight: cross-built binaries, .vsix, and bundled installer payloads must exist.
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         dist/windows/uninstall.exe dist/windows/token-refresher.exe \
         extensions/agentserver-app/agentserver-app-0.1.0.vsix \
         internal/ui/assets/dist/index.html \
         packaging/windows/install.ps1 \
         packaging/windows/ensure-vscode.ps1 \
         packaging/windows/ensure-codex-desktop.ps1 \
         packaging/windows/write-install-mode.ps1 \
         packaging/windows/machine.ps1 \
         packaging/windows/vscode-manifest.json \
         packaging/windows/ChineseSimplified.isl \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt \
         "$VSCODE_CACHE" \
         "$CODEX_DESKTOP_CACHE" \
         "$LOOM_DRIVER_CACHE" \
         "$LOOM_SLAVE_CACHE" \
         "$LOOM_DRIVER_SKILLS_CACHE" \
         "$SUPERPOWER_SKILLS_CACHE" \
         "$LOOM_DRIVER_CODEX_PROMPTS_CACHE" \
         "$CODEX_CACHE"; do
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
