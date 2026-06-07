#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

CODEX_RELEASE="rust-v0.136.0"
CODEX_ASSET="codex-x86_64-pc-windows-msvc.exe"
CODEX_URL="https://github.com/openai/codex/releases/download/$CODEX_RELEASE/$CODEX_ASSET"
CODEX_CACHE="dist/cache/$CODEX_RELEASE/$CODEX_ASSET"
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
    echo "ERROR: VS Code installer size mismatch: got $local_size want $VSCODE_SIZE" >&2
    exit 2
  fi
  local_sum=$(sha256sum "$VSCODE_CACHE.part" | awk '{print $1}')
  if [[ "$local_sum" != "$VSCODE_SHA256" ]]; then
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
         extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix \
         internal/ui/assets/dist/index.html \
         packaging/windows/install.ps1 \
         packaging/windows/ensure-vscode.ps1 \
         packaging/windows/ensure-codex-desktop.ps1 \
         packaging/windows/write-install-mode.ps1 \
         packaging/windows/vscode-manifest.json \
         packaging/windows/ChineseSimplified.isl \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt \
         "$VSCODE_CACHE" \
         "$CODEX_CACHE"; do
  if [[ ! -e "$f" ]]; then
    echo "missing: $f"
    case "$f" in
      internal/ui/assets/dist/*) echo "  hint: run 'make ui-build'" ;;
      dist/windows/*.exe)        echo "  hint: run 'make cross-windows'" ;;
      */agentserver-vscode-*.vsix) echo "  hint: run 'make ext-build'" ;;
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
