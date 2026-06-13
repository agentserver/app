#!/usr/bin/env bash
# Build a portable .zip distribution of 星池指挥官 for Windows.
# This is the Inno-Setup-free alternative used when no .exe-building
# toolchain is available on the dev host.
#
# Output: dist/agentserver-app-<ver>-portable.zip containing:
#   launcher.exe, onboarding-server.exe, agentctl.exe, open-folder.exe
#   uninstall.exe, token-refresher.exe
#   driver-agent.exe, slave-agent.exe
#   agentserver-app.vsix
#   codex-desktop-installer.exe (bundled to avoid winget Store execution during install)
#   icon.ico, install.ps1, ensure-vscode.ps1, ensure-codex-desktop.ps1
#   ensure-codex.ps1, write-install-mode.ps1, machine.ps1, codex-manifest.json
#   LICENSE.zh.txt, README.txt
#
# User flow on Windows:
#   1. Unzip
#   2. Right-click install.ps1 → "Run with PowerShell" (or via cmdline)
#   3. Double-click 星池指挥官 desktop shortcut
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="0.1.2"
OUT="dist"
STAGE="$OUT/agentserver-app-$VERSION-portable"
ZIP="$OUT/agentserver-app-$VERSION-portable.zip"

CODEX_DESKTOP_PRODUCT_ID="9PLM9XGG6VKS"
CODEX_DESKTOP_ASSET="Codex Installer.exe"
CODEX_DESKTOP_URL="https://get.microsoft.com/installer/download/$CODEX_DESKTOP_PRODUCT_ID?cid=website_cta_psi"
CODEX_DESKTOP_CACHE="$OUT/cache/codex-desktop/$CODEX_DESKTOP_PRODUCT_ID/$CODEX_DESKTOP_ASSET"
CODEX_DESKTOP_MIN_SIZE=65536
LOOM_RELEASE="v0.0.5"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"
LOOM_DRIVER_ASSET="driver-agent.windows-amd64.exe"
LOOM_DRIVER_SHA256="be3836eba3fabc5006d83a8edf687b0c0183e87beb493d2cb3c1799577f0c322"
LOOM_DRIVER_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_ASSET"
LOOM_SLAVE_ASSET="slave-agent.windows-amd64.exe"
LOOM_SLAVE_SHA256="8e0dfe1b7ce57dac387207f19ed7ebe4f2ab3a398990fcb3acc6c0c2a52bd27d"
LOOM_SLAVE_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_SLAVE_ASSET"
LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="4466b0342eaa90284dc4de0f0c03e6d08dbe02e4c12d0da6e7cb433c61ea1a0c"
LOOM_DRIVER_SKILLS_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_SKILLS_ASSET"
SUPERPOWER_SKILLS_CACHE="$OUT/cache/superpowers/driver-superpower-skills.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_ASSET="driver-codex-prompts.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_CODEX_PROMPTS_ASSET"

verify_sha256() {
  local path expected sum
  path="$1"
  expected="$2"
  [[ -f "$path" ]] || return 1
  sum=$(sha256sum "$path" | awk '{print $1}')
  [[ "$sum" == "$expected" ]]
}

verify_codex_desktop_installer() {
  local path size magic
  path="$1"
  [[ -f "$path" ]] || return 1
  size=$(stat -c%s "$path")
  (( size >= CODEX_DESKTOP_MIN_SIZE )) || return 1
  magic=$(head -c 2 "$path" 2>/dev/null || true)
  [[ "$magic" == "MZ" ]]
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

mkdir -p "$(dirname "$CODEX_DESKTOP_CACHE")"
rm -f "$CODEX_DESKTOP_CACHE" "$CODEX_DESKTOP_CACHE.part"
echo "Fetching Codex Desktop installer ..."
echo "  URL: $CODEX_DESKTOP_URL"
if ! curl --fail --location --retry 2 --retry-delay 2 --output "$CODEX_DESKTOP_CACHE.part" "$CODEX_DESKTOP_URL"; then
  rm -f "$CODEX_DESKTOP_CACHE.part"
  echo "ERROR: failed to download Codex Desktop installer" >&2
  exit 2
fi
if ! verify_codex_desktop_installer "$CODEX_DESKTOP_CACHE.part"; then
  rm -f "$CODEX_DESKTOP_CACHE.part"
  echo "ERROR: invalid Codex Desktop installer download" >&2
  exit 2
fi
mv "$CODEX_DESKTOP_CACHE.part" "$CODEX_DESKTOP_CACHE"
codex_desktop_size=$(stat -c%s "$CODEX_DESKTOP_CACHE")
echo "Codex Desktop installer: $codex_desktop_size bytes (fresh)"

download_loom_asset "$LOOM_DRIVER_ASSET" "$LOOM_DRIVER_CACHE" "$LOOM_DRIVER_SHA256"
download_loom_asset "$LOOM_SLAVE_ASSET" "$LOOM_SLAVE_CACHE" "$LOOM_SLAVE_SHA256"
download_loom_asset "$LOOM_DRIVER_SKILLS_ASSET" "$LOOM_DRIVER_SKILLS_CACHE" "$LOOM_DRIVER_SKILLS_SHA256"
python3 scripts/package-superpower-skills.py "$SUPERPOWER_SKILLS_CACHE"
python3 scripts/package-driver-codex-prompts.py "$LOOM_DRIVER_CODEX_PROMPTS_CACHE"

# Pre-flight
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         dist/windows/uninstall.exe dist/windows/token-refresher.exe \
         extensions/agentserver-app/agentserver-app-0.1.2.vsix \
         internal/ui/assets/dist/index.html \
         packaging/windows/install.ps1 \
         packaging/windows/install-driver-support.ps1 \
         packaging/windows/ensure-vscode.ps1 \
         packaging/windows/ensure-codex.ps1 \
         packaging/windows/codex-manifest.json \
         packaging/windows/ensure-codex-desktop.ps1 \
         packaging/windows/write-install-mode.ps1 \
         packaging/windows/machine.ps1 \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt \
         "$CODEX_DESKTOP_CACHE" \
         "$LOOM_DRIVER_CACHE" \
         "$LOOM_SLAVE_CACHE" \
         "$LOOM_DRIVER_SKILLS_CACHE" \
         "$SUPERPOWER_SKILLS_CACHE" \
         "$LOOM_DRIVER_CODEX_PROMPTS_CACHE"; do
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

rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"

# Binaries
cp dist/windows/launcher.exe          "$STAGE/"
cp dist/windows/onboarding-server.exe "$STAGE/"
cp dist/windows/agentctl.exe          "$STAGE/"
cp dist/windows/open-folder.exe       "$STAGE/"
cp dist/windows/uninstall.exe         "$STAGE/"
cp dist/windows/token-refresher.exe   "$STAGE/"
cp "$LOOM_DRIVER_CACHE"               "$STAGE/driver-agent.exe"
cp "$LOOM_SLAVE_CACHE"                "$STAGE/slave-agent.exe"
cp "$LOOM_DRIVER_SKILLS_CACHE"        "$STAGE/driver-skills.tar.gz"
cp "$SUPERPOWER_SKILLS_CACHE"         "$STAGE/driver-superpower-skills.tar.gz"
cp "$LOOM_DRIVER_CODEX_PROMPTS_CACHE" "$STAGE/driver-codex-prompts.tar.gz"

# Bundled Codex Desktop installer (avoids winget Store execution during install)
cp "$CODEX_DESKTOP_CACHE" "$STAGE/codex-desktop-installer.exe"

# VS Code extension
cp extensions/agentserver-app/agentserver-app-0.1.2.vsix \
   "$STAGE/agentserver-app.vsix"

# Resources
cp packaging/windows/install.ps1      "$STAGE/"
cp packaging/windows/install-driver-support.ps1 "$STAGE/"
cp packaging/windows/ensure-vscode.ps1 "$STAGE/"
cp packaging/windows/ensure-codex.ps1 "$STAGE/"
cp packaging/windows/codex-manifest.json "$STAGE/"
cp packaging/windows/ensure-codex-desktop.ps1 "$STAGE/"
cp packaging/windows/write-install-mode.ps1 "$STAGE/"
cp packaging/windows/machine.ps1 "$STAGE/"
cp packaging/windows/icon.ico         "$STAGE/"
cp packaging/windows/LICENSE.zh.txt   "$STAGE/"

# Plain-English readme
cat > "$STAGE/README.txt" <<'EOF'
星池指挥官 portable — installation
==================================

1) Right-click `install.ps1` and choose "Run with PowerShell"
   (or open PowerShell and run:
    powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1)

2) Wait for "Install complete." The installer downloads the Codex runtime from
   domestic npm mirrors and installs Codex Desktop with the bundled Microsoft installer.
   To install the simplified VS Code interface instead, run:
     powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -MinimalVSCode

   The simplified VS Code mode downloads the Microsoft Store bootstrapper during install.

3) Double-click the "星池指挥官" shortcut on your desktop.
   The first launch opens a configuration wizard in your browser.

To uninstall:
   Open "Apps & features" → search "星池指挥官" → Uninstall.
   Or run:
     "%LOCALAPPDATA%\Programs\agentserver-app\uninstall.exe" --silent

See LICENSE.zh.txt for what gets written to your machine.
EOF

# Zip it (use python3 since `zip` isn't always available; bsdtar/7z fallback)
if command -v zip >/dev/null 2>&1; then
  (cd "$OUT" && zip -qr "$(basename "$ZIP")" "$(basename "$STAGE")")
else
  python3 - "$OUT" "$(basename "$ZIP")" "$(basename "$STAGE")" <<'PYEOF'
import os, sys, zipfile, pathlib
out_dir, zip_name, stage_name = sys.argv[1:]
zip_path = os.path.join(out_dir, zip_name)
stage_path = os.path.join(out_dir, stage_name)
with zipfile.ZipFile(zip_path, 'w', zipfile.ZIP_DEFLATED) as zf:
    for p in pathlib.Path(stage_path).rglob('*'):
        zf.write(p, p.relative_to(out_dir))
PYEOF
fi

echo "Built: $ZIP"
ls -la "$ZIP"
