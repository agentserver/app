OUT="${OUT:-dist}"
VERSION="${VERSION:-}"

CHATGPT_DESKTOP_PRODUCT_ID="9PLM9XGG6VKS"
CHATGPT_DESKTOP_ASSET="ChatGPT Installer.exe"
CHATGPT_DESKTOP_URL="https://get.microsoft.com/installer/download/$CHATGPT_DESKTOP_PRODUCT_ID?cid=website_cta_psi"
CHATGPT_DESKTOP_CACHE="$OUT/cache/chatgpt-desktop/$CHATGPT_DESKTOP_PRODUCT_ID/$CHATGPT_DESKTOP_ASSET"
CHATGPT_DESKTOP_MANIFEST="$OUT/cache/chatgpt-desktop/$CHATGPT_DESKTOP_PRODUCT_ID/chatgpt-desktop-installer.manifest.json"
CHATGPT_DESKTOP_SIGNATURE_VERIFIER="packaging/windows/verify-chatgpt-desktop-installer.ps1"
CHATGPT_DESKTOP_MIN_SIZE=65536
LOOM_RELEASE="v0.0.10"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"
LOOM_DRIVER_ASSET="driver-agent.windows-amd64.exe"
LOOM_DRIVER_SHA256="411ab9e7ed586a7db5cb51f4948acf1c880936ef7643db94044115340e8df527"
LOOM_DRIVER_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_ASSET"
LOOM_SLAVE_ASSET="slave-agent.windows-amd64.exe"
LOOM_SLAVE_SHA256="ac6401a709ff2addc1f74aaaa3ac38a2d5f2807d1ceaf1fb71a52d48a3c34d3b"
LOOM_SLAVE_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_SLAVE_ASSET"
LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="f9641c17e0a5105b4f97adf9ce70e186ee849fc4f03ad13fe3460cb54ec02ba9"
LOOM_DRIVER_SKILLS_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_SKILLS_ASSET"
SUPERPOWER_SKILLS_CACHE="$OUT/cache/superpowers/driver-superpower-skills.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_ASSET="driver-codex-prompts.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_CACHE="$OUT/cache/loom/$LOOM_RELEASE/$LOOM_DRIVER_CODEX_PROMPTS_ASSET"

WINDOWS_PACKAGE_REQUIRED_FILES=(
  "dist/windows/launcher.exe"
  "dist/windows/onboarding-server.exe"
  "dist/windows/agentctl.exe"
  "dist/windows/codex-debug-wrapper.exe"
  "dist/windows/open-folder.exe"
  "dist/windows/uninstall.exe"
  "dist/windows/token-refresher.exe"
  "extensions/agentserver-app/agentserver-app-$VERSION.vsix"
  "internal/ui/assets/dist/index.html"
  "packaging/windows/install.ps1"
  "packaging/windows/install-driver-support.ps1"
  "packaging/windows/ensure-vscode.ps1"
  "packaging/windows/ensure-codex.ps1"
  "packaging/windows/codex-manifest.json"
  "packaging/windows/ensure-codex-desktop.ps1"
  "$CHATGPT_DESKTOP_SIGNATURE_VERIFIER"
  "internal/codexdesktop/detect_windows.ps1"
  "packaging/windows/ensure-opencode-desktop.ps1"
  "packaging/windows/write-install-mode.ps1"
  "packaging/windows/machine.ps1"
  "packaging/windows/ChineseSimplified.isl"
  "packaging/windows/icon.ico"
  "packaging/windows/LICENSE.zh.txt"
  "$CHATGPT_DESKTOP_CACHE"
  "$CHATGPT_DESKTOP_MANIFEST"
  "$LOOM_DRIVER_CACHE"
  "$LOOM_SLAVE_CACHE"
  "$LOOM_DRIVER_SKILLS_CACHE"
  "$SUPERPOWER_SKILLS_CACHE"
  "$LOOM_DRIVER_CODEX_PROMPTS_CACHE"
)

PORTABLE_PAYLOADS=(
  "dist/windows/launcher.exe::launcher.exe"
  "dist/windows/onboarding-server.exe::onboarding-server.exe"
  "dist/windows/agentctl.exe::agentctl.exe"
  "dist/windows/codex-debug-wrapper.exe::codex-debug-wrapper.exe"
  "dist/windows/open-folder.exe::open-folder.exe"
  "dist/windows/uninstall.exe::uninstall.exe"
  "dist/windows/token-refresher.exe::token-refresher.exe"
  "$LOOM_DRIVER_CACHE::driver-agent.exe"
  "$LOOM_SLAVE_CACHE::slave-agent.exe"
  "$LOOM_DRIVER_SKILLS_CACHE::driver-skills.tar.gz"
  "$SUPERPOWER_SKILLS_CACHE::driver-superpower-skills.tar.gz"
  "$LOOM_DRIVER_CODEX_PROMPTS_CACHE::driver-codex-prompts.tar.gz"
  "$CHATGPT_DESKTOP_CACHE::chatgpt-desktop-installer.exe"
  "$CHATGPT_DESKTOP_MANIFEST::chatgpt-desktop-installer.manifest.json"
  "extensions/agentserver-app/agentserver-app-$VERSION.vsix::agentserver-app.vsix"
  "packaging/windows/install.ps1::install.ps1"
  "packaging/windows/install-driver-support.ps1::install-driver-support.ps1"
  "packaging/windows/ensure-vscode.ps1::ensure-vscode.ps1"
  "packaging/windows/ensure-codex.ps1::ensure-codex.ps1"
  "packaging/windows/codex-manifest.json::codex-manifest.json"
  "packaging/windows/ensure-codex-desktop.ps1::ensure-codex-desktop.ps1"
  "$CHATGPT_DESKTOP_SIGNATURE_VERIFIER::verify-chatgpt-desktop-installer.ps1"
  "internal/codexdesktop/detect_windows.ps1::codex-desktop-detect.ps1"
  "packaging/windows/ensure-opencode-desktop.ps1::ensure-opencode-desktop.ps1"
  "packaging/windows/write-install-mode.ps1::write-install-mode.ps1"
  "packaging/windows/machine.ps1::machine.ps1"
  "packaging/windows/icon.ico::icon.ico"
  "packaging/windows/LICENSE.zh.txt::LICENSE.zh.txt"
)

verify_sha256() {
  local path expected sum
  path="$1"
  expected="$2"
  [[ -f "$path" ]] || return 1
  sum=$(sha256sum "$path" | awk '{print $1}')
  [[ "$sum" == "$expected" ]]
}

verify_chatgpt_desktop_installer() {
  local path size magic
  path="$1"
  [[ -f "$path" ]] || return 1
  size=$(stat -c%s "$path")
  (( size >= CHATGPT_DESKTOP_MIN_SIZE )) || return 1
  magic=$(head -c 2 "$path" 2>/dev/null || true)
  [[ "$magic" == "MZ" ]] || return 1
  verify_chatgpt_desktop_signature "$path"
}

verify_chatgpt_desktop_signature() {
  local path ps
  path="$1"
  if command -v pwsh >/dev/null 2>&1; then
    ps="pwsh"
  elif command -v powershell.exe >/dev/null 2>&1; then
    ps="powershell.exe"
  else
    # PowerShell is unavailable on this host (e.g. Linux packaging box). The
    # Authenticode signature is re-verified at install time by
    # ensure-codex-desktop.ps1 on the Windows target, so skip it here rather
    # than failing the cross-host build. The size + MZ-magic checks above still run.
    echo "WARNING: PowerShell not available; skipping ChatGPT desktop Authenticode check on this host (re-verified at install time)" >&2
    return 0
  fi
  "$ps" -NoProfile -File "$CHATGPT_DESKTOP_SIGNATURE_VERIFIER" -Path "$path"
}

write_chatgpt_desktop_manifest() {
  local installer_path manifest_path size sha
  installer_path="$1"
  manifest_path="$2"
  command -v jq >/dev/null || { echo "jq required to write ChatGPT desktop installer manifest" >&2; return 2; }
  size=$(stat -c%s "$installer_path") || return 1
  sha=$(sha256sum "$installer_path" | awk '{print $1}') || return 1
  jq -n \
    --arg product_id "$CHATGPT_DESKTOP_PRODUCT_ID" \
    --arg source_url "$CHATGPT_DESKTOP_URL" \
    --arg sha256 "$sha" \
    --argjson size "$size" \
    '{product_id:$product_id, source_url:$source_url, sha256:$sha256, size:$size}' \
    >"$manifest_path"
}

verify_chatgpt_desktop_pair() {
  local installer_path manifest_path product_id source_url expected_sha expected_size actual_sha actual_size
  installer_path="$1"
  manifest_path="$2"
  [[ -f "$installer_path" && -f "$manifest_path" ]] || return 1
  command -v jq >/dev/null || return 1
  product_id=$(jq -er '.product_id | strings' "$manifest_path") || return 1
  source_url=$(jq -er '.source_url | strings' "$manifest_path") || return 1
  expected_sha=$(jq -er '.sha256 | strings' "$manifest_path") || return 1
  expected_size=$(jq -er '.size | numbers' "$manifest_path") || return 1
  [[ "$product_id" == "$CHATGPT_DESKTOP_PRODUCT_ID" ]] || return 1
  [[ "$source_url" == "$CHATGPT_DESKTOP_URL" ]] || return 1
  actual_sha=$(sha256sum "$installer_path" | awk '{print $1}') || return 1
  actual_size=$(stat -c%s "$installer_path") || return 1
  [[ "${actual_sha,,}" == "${expected_sha,,}" ]] || return 1
  [[ "$actual_size" == "$expected_size" ]] || return 1
}

restore_chatgpt_desktop_pair() {
  local backup_dir dest_installer dest_manifest had_installer had_manifest
  backup_dir="$1"
  dest_installer="$2"
  dest_manifest="$3"
  had_installer="$4"
  had_manifest="$5"
  rm -f "$dest_installer" "$dest_manifest"
  if [[ "$had_installer" == "1" ]]; then
    mv -f "$backup_dir/installer" "$dest_installer" || return 1
  fi
  if [[ "$had_manifest" == "1" ]]; then
    mv -f "$backup_dir/manifest" "$dest_manifest" || return 1
  fi
}

publish_chatgpt_desktop_pair() {
  local staged_installer staged_manifest dest_installer dest_manifest dest_dir backup_dir
  local had_installer=0 had_manifest=0 status=0
  staged_installer="$1"
  staged_manifest="$2"
  dest_installer="$3"
  dest_manifest="$4"
  [[ -f "$staged_installer" && -f "$staged_manifest" ]] || return 1
  dest_dir=$(dirname "$dest_installer")
  [[ "$dest_dir" == "$(dirname "$dest_manifest")" ]] || return 1
  mkdir -p "$dest_dir" || return 1
  backup_dir=$(mktemp -d "$dest_dir/.chatgpt-pair-backup.XXXXXX") || return 1

  if [[ -e "$dest_installer" ]]; then
    mv "$dest_installer" "$backup_dir/installer" || { rm -rf "$backup_dir"; return 1; }
    had_installer=1
  fi
  if [[ -e "$dest_manifest" ]]; then
    if ! mv "$dest_manifest" "$backup_dir/manifest"; then
      # The old manifest is still at its published path because this
      # same-filesystem rename failed. Restore only the installer that was
      # already moved; the generic pair restore would delete that manifest.
      if [[ "$had_installer" == "0" ]] || mv -f "$backup_dir/installer" "$dest_installer"; then
        rm -rf "$backup_dir"
      else
        echo "ERROR: failed to restore ChatGPT desktop cache; recovery files remain in $backup_dir" >&2
      fi
      return 1
    fi
    had_manifest=1
  fi

  if ! mv "$staged_installer" "$dest_installer"; then
    status=1
  elif [[ "${CHATGPT_DESKTOP_TEST_FAIL_AFTER_INSTALLER_PUBLISH:-}" == "1" ]]; then
    status=1
  elif ! mv "$staged_manifest" "$dest_manifest"; then
    status=1
  elif ! verify_chatgpt_desktop_pair "$dest_installer" "$dest_manifest"; then
    status=1
  fi

  if [[ "$status" != "0" ]]; then
    if restore_chatgpt_desktop_pair "$backup_dir" "$dest_installer" "$dest_manifest" "$had_installer" "$had_manifest"; then
      rm -rf "$backup_dir"
    else
      echo "ERROR: failed to restore ChatGPT desktop cache; recovery files remain in $backup_dir" >&2
    fi
    return 1
  fi
  rm -rf "$backup_dir"
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

fetch_windows_package_assets() {
  local chatgpt_stage chatgpt_installer_tmp chatgpt_manifest_tmp chatgpt_desktop_size
  mkdir -p "$(dirname "$CHATGPT_DESKTOP_CACHE")"
  chatgpt_stage=$(mktemp -d "$(dirname "$CHATGPT_DESKTOP_CACHE")/.chatgpt-stage.XXXXXX")
  chatgpt_installer_tmp="$chatgpt_stage/installer.exe"
  chatgpt_manifest_tmp="$chatgpt_stage/manifest.json"
  echo "Fetching ChatGPT desktop installer ..."
  echo "  URL: $CHATGPT_DESKTOP_URL"
  if ! curl --fail --location --retry 2 --retry-delay 2 --output "$chatgpt_installer_tmp" "$CHATGPT_DESKTOP_URL"; then
    rm -rf "$chatgpt_stage"
    echo "ERROR: failed to download ChatGPT desktop installer" >&2
    exit 2
  fi
  if ! verify_chatgpt_desktop_installer "$chatgpt_installer_tmp"; then
    rm -rf "$chatgpt_stage"
    echo "ERROR: invalid ChatGPT desktop installer download" >&2
    exit 2
  fi
  if ! write_chatgpt_desktop_manifest "$chatgpt_installer_tmp" "$chatgpt_manifest_tmp" ||
     ! verify_chatgpt_desktop_pair "$chatgpt_installer_tmp" "$chatgpt_manifest_tmp"; then
    rm -rf "$chatgpt_stage"
    echo "ERROR: failed to create a verified ChatGPT desktop installer manifest" >&2
    exit 2
  fi
  if ! publish_chatgpt_desktop_pair \
      "$chatgpt_installer_tmp" "$chatgpt_manifest_tmp" \
      "$CHATGPT_DESKTOP_CACHE" "$CHATGPT_DESKTOP_MANIFEST"; then
    rm -rf "$chatgpt_stage"
    echo "ERROR: failed to publish the ChatGPT desktop installer and manifest pair" >&2
    exit 2
  fi
  rm -rf "$chatgpt_stage"
  chatgpt_desktop_size=$(stat -c%s "$CHATGPT_DESKTOP_CACHE")
  echo "ChatGPT desktop installer: $chatgpt_desktop_size bytes (fresh)"

  download_loom_asset "$LOOM_DRIVER_ASSET" "$LOOM_DRIVER_CACHE" "$LOOM_DRIVER_SHA256"
  download_loom_asset "$LOOM_SLAVE_ASSET" "$LOOM_SLAVE_CACHE" "$LOOM_SLAVE_SHA256"
  download_loom_asset "$LOOM_DRIVER_SKILLS_ASSET" "$LOOM_DRIVER_SKILLS_CACHE" "$LOOM_DRIVER_SKILLS_SHA256"
  python3 scripts/package-superpower-skills.py "$SUPERPOWER_SKILLS_CACHE"
  python3 scripts/package-driver-codex-prompts.py "$LOOM_DRIVER_CODEX_PROMPTS_CACHE"
}

check_windows_package_required_files() {
  local f
  for f in "${WINDOWS_PACKAGE_REQUIRED_FILES[@]}"; do
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
}

copy_portable_payloads() {
  local stage entry src dest
  stage="$1"
  for entry in "${PORTABLE_PAYLOADS[@]}"; do
    src="${entry%%::*}"
    dest="${entry#*::}"
    cp "$src" "$stage/$dest"
  done
}

# render_latest_json emits both latest-cdn.json and latest-github.json
# from a built installer. Requires jq (composes JSON structurally so
# free-form "notes" — quotes, backslashes, newlines — round-trips safely).
#
# Usage:
#   render_latest_json <installer_path> <version> [notes]
#
# Env overrides:
#   UPGRADE_GITHUB_REPO  (default: agentserver/app)
render_latest_json() {
  local installer_path="$1"
  local version="$2"
  local notes="${3:-}"
  command -v jq >/dev/null || { echo "jq required" >&2; return 2; }

  local size sha installer_name dist_dir
  # GNU stat first (Linux/Git-Bash on Windows CI), BSD stat fallback (macOS).
  size=$(stat -c%s "$installer_path" 2>/dev/null || stat -f%z "$installer_path")
  sha=$(sha256sum "$installer_path" | cut -d' ' -f1)
  installer_name=$(basename "$installer_path")
  dist_dir=$(dirname "$installer_path")

  local owner_repo="${UPGRADE_GITHUB_REPO:-agentserver/app}"
  local tag="v${version}"
  local cdn_url="https://assets.agent.cs.ac.cn/agentserver-app/windows/${installer_name}"
  local gh_url="https://github.com/${owner_repo}/releases/download/${tag}/${installer_name}"

  jq -n \
    --arg version "$version" \
    --arg url "$cdn_url" \
    --arg sha "$sha" \
    --arg notes "$notes" \
    --argjson size "$size" \
    '{version:$version, url:$url, sha256:$sha, size:$size, notes:$notes}' \
    > "${dist_dir}/latest-cdn.json"

  jq -n \
    --arg version "$version" \
    --arg url "$gh_url" \
    --arg sha "$sha" \
    --arg notes "$notes" \
    --argjson size "$size" \
    '{version:$version, url:$url, sha256:$sha, size:$size, notes:$notes}' \
    > "${dist_dir}/latest-github.json"
}
