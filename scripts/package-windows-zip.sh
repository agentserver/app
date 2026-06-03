#!/usr/bin/env bash
# Build a portable .zip distribution of agentserver-vscode for Windows.
# This is the Inno-Setup-free alternative used when no .exe-building
# toolchain is available on the dev host.
#
# Output: dist/agentserver-vscode-<ver>-portable.zip containing:
#   launcher.exe, onboarding-server.exe, agentctl.exe, open-folder.exe
#   agentserver-vscode.vsix
#   icon.ico, install.ps1, LICENSE.zh.txt, README.txt
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

# Pre-flight
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix \
         packaging/windows/install.ps1 \
         packaging/windows/icon.ico \
         packaging/windows/LICENSE.zh.txt; do
  [[ -e "$f" ]] || { echo "missing: $f"; exit 1; }
done

rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"

# Binaries
cp dist/windows/launcher.exe          "$STAGE/"
cp dist/windows/onboarding-server.exe "$STAGE/"
cp dist/windows/agentctl.exe          "$STAGE/"
cp dist/windows/open-folder.exe       "$STAGE/"

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
