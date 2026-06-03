#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Pre-flight: cross-built binaries + .vsix must exist.
for f in dist/windows/launcher.exe dist/windows/onboarding-server.exe \
         dist/windows/agentctl.exe dist/windows/open-folder.exe \
         extensions/agentserver-vscode/agentserver-vscode-0.1.0.vsix; do
  if [[ ! -e "$f" ]]; then
    echo "missing: $f"
    echo "Run 'make cross-windows ext-build' first."
    exit 1
  fi
done

# Find ISCC.exe (Inno Setup). Local install (Windows) or wine.
ISCC=""
if command -v ISCC.exe >/dev/null 2>&1; then
  ISCC="ISCC.exe"
elif command -v iscc >/dev/null 2>&1; then
  ISCC="iscc"
elif command -v wine >/dev/null 2>&1 && \
     [[ -f "$HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe" ]]; then
  ISCC="wine $HOME/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe"
else
  echo "Inno Setup not found. Install on Windows, or install via Wine:"
  echo "  wine innosetup-6.x.exe /VERYSILENT"
  exit 2
fi

cd packaging/windows
mkdir -p Output
$ISCC installer.iss
ls -la Output/
