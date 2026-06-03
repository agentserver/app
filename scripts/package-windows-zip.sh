#!/usr/bin/env bash
# Build a portable .zip distribution of agentserver-vscode for Windows.
# This is the Inno-Setup-free alternative used when no .exe-building
# toolchain is available on the dev host.
#
# Output: dist/agentserver-vscode-<ver>-portable.zip containing:
#   launcher.exe, onboarding-server.exe, agentctl.exe, open-folder.exe
#   agentserver-vscode.vsix
#   codex.exe  (246MB, bundled to avoid GitHub download from CN)
#   icon.ico, install.ps1, LICENSE.zh.txt, README.txt
#
# codex.exe is cached in dist/cache/ across builds so re-packaging doesn't
# re-fetch the 246MB binary. Delete dist/cache/ to force re-download.
#
# User flow on Windows:
#   1. Unzip
#   2. Right-click install.ps1 → "Run with PowerShell" (or via cmdline)
#   3. Double-click agentserver-vscode desktop shortcut
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="0.1.0"
OUT="dist"
STAGE="$OUT/agentserver-vscode-$VERSION-portable"
ZIP="$OUT/agentserver-vscode-$VERSION-portable.zip"

CODEX_RELEASE="rust-v0.136.0"
CODEX_ASSET="codex-x86_64-pc-windows-msvc.exe"
CODEX_URL="https://github.com/openai/codex/releases/download/$CODEX_RELEASE/$CODEX_ASSET"
CODEX_CACHE="$OUT/cache/$CODEX_RELEASE/$CODEX_ASSET"

# Cache codex.exe so re-packaging doesn't re-fetch 246MB
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
  echo "cached at: $CODEX_CACHE"
fi
codex_size=$(stat -c%s "$CODEX_CACHE")
echo "codex.exe: $codex_size bytes (cached)"

# Pre-flight
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix \
         packaging/windows/install.ps1 \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt \
         "$CODEX_CACHE"; do
  [[ -e "$f" ]] || { echo "missing: $f"; exit 1; }
done

rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"

# Binaries
cp dist/windows/launcher.exe          "$STAGE/"
cp dist/windows/onboarding-server.exe "$STAGE/"
cp dist/windows/agentctl.exe          "$STAGE/"
cp dist/windows/open-folder.exe       "$STAGE/"

# Bundled codex.exe (avoids GitHub round-trip during install)
cp "$CODEX_CACHE" "$STAGE/codex.exe"

# VS Code extension
cp extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix \
   "$STAGE/agentserver-vscode.vsix"

# Resources
cp packaging/windows/install.ps1      "$STAGE/"
cp packaging/windows/icon.ico         "$STAGE/"
cp packaging/windows/LICENSE.zh.txt   "$STAGE/"

# Plain-English readme
cat > "$STAGE/README.txt" <<'EOF'
agentserver-vscode portable — installation
==========================================

1) Right-click `install.ps1` and choose "Run with PowerShell"
   (or open PowerShell and run:
    powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1)

2) Wait for "Install complete."

3) Double-click the "agentserver-vscode" shortcut on your desktop.
   The first launch opens a configuration wizard in your browser.

To uninstall:
   Open "Apps & features" → search "agentserver-vscode" → Uninstall.
   Or run: powershell -NoProfile -ExecutionPolicy Bypass -File \
     "%LOCALAPPDATA%\Programs\agentserver-vscode\install.ps1" -Uninstall -Silent

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
