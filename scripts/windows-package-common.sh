CODEX_DESKTOP_PRODUCT_ID="9PLM9XGG6VKS"
CODEX_DESKTOP_ASSET="Codex Installer.exe"
CODEX_DESKTOP_URL="https://get.microsoft.com/installer/download/$CODEX_DESKTOP_PRODUCT_ID?cid=website_cta_psi"
CODEX_DESKTOP_CACHE="$OUT/cache/codex-desktop/$CODEX_DESKTOP_PRODUCT_ID/$CODEX_DESKTOP_ASSET"
CODEX_DESKTOP_MIN_SIZE=65536
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
  "packaging/windows/ensure-opencode-desktop.ps1"
  "packaging/windows/write-install-mode.ps1"
  "packaging/windows/machine.ps1"
  "packaging/windows/ChineseSimplified.isl"
  "packaging/windows/icon.ico"
  "packaging/windows/LICENSE.zh.txt"
  "$CODEX_DESKTOP_CACHE"
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
  "dist/windows/open-folder.exe::open-folder.exe"
  "dist/windows/uninstall.exe::uninstall.exe"
  "dist/windows/token-refresher.exe::token-refresher.exe"
  "$LOOM_DRIVER_CACHE::driver-agent.exe"
  "$LOOM_SLAVE_CACHE::slave-agent.exe"
  "$LOOM_DRIVER_SKILLS_CACHE::driver-skills.tar.gz"
  "$SUPERPOWER_SKILLS_CACHE::driver-superpower-skills.tar.gz"
  "$LOOM_DRIVER_CODEX_PROMPTS_CACHE::driver-codex-prompts.tar.gz"
  "$CODEX_DESKTOP_CACHE::codex-desktop-installer.exe"
  "extensions/agentserver-app/agentserver-app-$VERSION.vsix::agentserver-app.vsix"
  "packaging/windows/install.ps1::install.ps1"
  "packaging/windows/install-driver-support.ps1::install-driver-support.ps1"
  "packaging/windows/ensure-vscode.ps1::ensure-vscode.ps1"
  "packaging/windows/ensure-codex.ps1::ensure-codex.ps1"
  "packaging/windows/codex-manifest.json::codex-manifest.json"
  "packaging/windows/ensure-codex-desktop.ps1::ensure-codex-desktop.ps1"
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

verify_codex_desktop_installer() {
  local path size magic
  path="$1"
  [[ -f "$path" ]] || return 1
  size=$(stat -c%s "$path")
  (( size >= CODEX_DESKTOP_MIN_SIZE )) || return 1
  magic=$(head -c 2 "$path" 2>/dev/null || true)
  [[ "$magic" == "MZ" ]] || return 1
  verify_codex_desktop_signature "$path"
}

verify_codex_desktop_signature() {
  local path ps script
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
    echo "WARNING: PowerShell not available; skipping Codex Desktop Authenticode check on this host (re-verified at install time)" >&2
    return 0
  fi
  script='param([string]$Path)
$sig = Get-AuthenticodeSignature -FilePath $Path
if ($sig.Status -ne "Valid") { throw "Codex Desktop installer Authenticode signature is $($sig.Status)" }
if ($null -eq $sig.SignerCertificate) { throw "Codex Desktop installer has no signer certificate" }
$subject = $sig.SignerCertificate.Subject
if ($subject -notmatch "O=Microsoft Corporation" -and $subject -notmatch "Microsoft Corporation") { throw "Codex Desktop installer signer is not Microsoft Corporation: $subject" }
$chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
$chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
if (-not $chain.Build($sig.SignerCertificate)) {
  $statuses = ($chain.ChainStatus | ForEach-Object { $_.Status }) -join ", "
  throw "Codex Desktop installer signer chain is invalid: $statuses"
}
$chainSubjects = @($chain.ChainElements | ForEach-Object { $_.Certificate.Subject })
if (-not ($chainSubjects -match "Microsoft")) { throw "Codex Desktop installer signer chain is not Microsoft" }'
  "$ps" -NoProfile -Command "$script" "$path"
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
  mkdir -p "$(dirname "$CODEX_DESKTOP_CACHE")"
  codex_desktop_tmp=$(mktemp "$CODEX_DESKTOP_CACHE.part.XXXXXX")
  echo "Fetching Codex Desktop installer ..."
  echo "  URL: $CODEX_DESKTOP_URL"
  if ! curl --fail --location --retry 2 --retry-delay 2 --output "$codex_desktop_tmp" "$CODEX_DESKTOP_URL"; then
    rm -f "$codex_desktop_tmp"
    echo "ERROR: failed to download Codex Desktop installer" >&2
    exit 2
  fi
  if ! verify_codex_desktop_installer "$codex_desktop_tmp"; then
    rm -f "$codex_desktop_tmp"
    echo "ERROR: invalid Codex Desktop installer download" >&2
    exit 2
  fi
  mv -f "$codex_desktop_tmp" "$CODEX_DESKTOP_CACHE"
  codex_desktop_size=$(stat -c%s "$CODEX_DESKTOP_CACHE")
  echo "Codex Desktop installer: $codex_desktop_size bytes (fresh)"

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
