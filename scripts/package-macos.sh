#!/usr/bin/env bash
# 组装 星池指挥官.app 并生成 DMG。仅在 macOS 上运行（依赖 CGo + iconutil/hdiutil/codesign）。
set -euo pipefail

VERSION="${VERSION:-1.0.0}"
APP_NAME="星池指挥官"
APP_INTERNAL="星池指挥官.app"
STAGE="dist/macos/stage"
DMG="dist/macos/${APP_NAME}-${VERSION}-universal.dmg"
MACOS_DIR="${STAGE}/${APP_INTERNAL}/Contents/MacOS"
RES_DIR="${STAGE}/${APP_INTERNAL}/Contents/Resources"

echo "==> [1/8] build universal binaries"
mkdir -p dist/macos/bin
for cmd in launcher open-folder token-refresher agentctl uninstall; do
  echo "  building $cmd (universal)"
  GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "dist/macos/bin/${cmd}.arm64" ./cmd/$cmd
  GOARCH=amd64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "dist/macos/bin/${cmd}.amd64" ./cmd/$cmd
  lipo -create -output "dist/macos/bin/$cmd" "dist/macos/bin/${cmd}.arm64" "dist/macos/bin/${cmd}.amd64"
  rm "dist/macos/bin/${cmd}.arm64" "dist/macos/bin/${cmd}.amd64"
done

echo "==> [2/8] fetch driver-agent / slave-agent (darwin, lipo universal)"
bash scripts/fetch-loom-darwin.sh

echo "==> [3/8] assemble .app layout"
rm -rf "${STAGE}"
mkdir -p "${MACOS_DIR}" "${RES_DIR}"
install -m 0755 dist/macos/bin/{launcher,open-folder,token-refresher,agentctl,uninstall} "${MACOS_DIR}/"
install -m 0755 dist/macos/bin/{driver-agent,slave-agent} "${MACOS_DIR}/"
cp packaging/macos/Info.plist "${STAGE}/${APP_INTERNAL}/Contents/Info.plist"
cp packaging/macos/icon.icns "${RES_DIR}/icon.icns"
cp packaging/macos/icon.png "${RES_DIR}/icon.png"
cp packaging/macos/icon-template.png "${RES_DIR}/icon-template.png"
cp packaging/macos/LICENSE.zh.txt "${RES_DIR}/"
cp packaging/macos/codex-manifest-darwin-arm64.json "${RES_DIR}/"
cp packaging/macos/codex-manifest-darwin-amd64.json "${RES_DIR}/"
install -m 0644 dist/agentserver-app.vsix "${RES_DIR}/agentserver-app.vsix" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-skills.tar.gz "${RES_DIR}/" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-superpower-skills.tar.gz "${RES_DIR}/" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-codex-prompts.tar.gz "${RES_DIR}/" || true
cp -R "packaging/macos/用星池指挥官打开.workflow" "${RES_DIR}/"

echo "==> [4/8] write initial install-mode.json"
printf '{"frontend_mode":"codex_desktop"}\n' > "${MACOS_DIR}/install-mode.json"

echo "==> [5/8] sign (ad-hoc by default; set MACOS_SIGN_IDENTITY for Developer ID)"
if [[ -n "${MACOS_SIGN_IDENTITY:-}" ]]; then
  codesign --deep --force --options runtime --sign "$MACOS_SIGN_IDENTITY" "${STAGE}/${APP_INTERNAL}"
  xcrun notarytool submit "${STAGE}/${APP_INTERNAL}" --keychain-profile "${MACOS_NOTARY_PROFILE:-}" --wait || true
  xcrun stapler staple "${STAGE}/${APP_INTERNAL}" || true
else
  codesign --deep --force --sign - "${STAGE}/${APP_INTERNAL}"
fi

echo "==> [6/8] build DMG (drag-to-Applications layout)"
mkdir -p dist/macos/dmg
cp -R "${STAGE}/${APP_INTERNAL}" dist/macos/dmg/
ln -sf /Applications dist/macos/dmg/Applications
rm -f "${DMG}"
hdiutil create -volname "${APP_NAME}" -srcfolder dist/macos/dmg -fs HFS+ -format UDZO "${DMG}"
if [[ -n "${MACOS_SIGN_IDENTITY:-}" ]]; then
  codesign --sign "$MACOS_SIGN_IDENTITY" "${DMG}"
  xcrun notarytool submit "${DMG}" --keychain-profile "${MACOS_NOTARY_PROFILE:-}" --wait || true
  xcrun stapler staple "${DMG}" || true
fi

echo "==> [7/8] sha256 sidecar"
shasum -a 256 "${DMG}" | awk '{print $1}' > "${DMG}.sha256"

echo "==> [8/8] done"
ls -lh "${DMG}" "${DMG}.sha256"
