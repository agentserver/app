#!/usr/bin/env bash
# Build a portable .zip distribution of 星池指挥官 for Windows.
# This is the Inno-Setup-free alternative used when no .exe-building
# toolchain is available on the dev host.
#
# Output: dist/agentserver-app-<ver>-portable.zip containing:
#   launcher.exe, onboarding-server.exe, agentctl.exe, codex-debug-wrapper.exe
#   open-folder.exe, uninstall.exe, token-refresher.exe
#   driver-agent.exe, slave-agent.exe
#   agentserver-app.vsix
#   codex-desktop-installer.exe (bundled to avoid winget Store execution during install)
#   icon.ico, install.ps1, ensure-vscode.ps1, ensure-codex-desktop.ps1
#   ensure-opencode-desktop.ps1
#   ensure-codex.ps1, write-install-mode.ps1, machine.ps1, codex-manifest.json
#   LICENSE.zh.txt, README.txt
#
# User flow on Windows:
#   1. Unzip
#   2. Right-click install.ps1 -> "Run with PowerShell" (or via cmdline)
#   3. Double-click 星池指挥官 desktop shortcut
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="0.1.8"
OUT="dist"
STAGE="$OUT/agentserver-app-$VERSION-portable"
ZIP="$OUT/agentserver-app-$VERSION-portable.zip"
source scripts/windows-package-common.sh

fetch_windows_package_assets
check_windows_package_required_files

rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"
copy_portable_payloads "$STAGE"

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

   To install the OpenCode Desktop interface instead, run:
     powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -OpenCodeDesktop

   The OpenCode Desktop mode downloads the latest official Windows installer during
   install. The simplified VS Code mode downloads the Microsoft Store bootstrapper
   during install.

3) Double-click the "星池指挥官" shortcut on your desktop.
   The first launch opens a configuration wizard in your browser.

To uninstall:
   Open "Apps & features" -> search "星池指挥官" -> Uninstall.
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
