LOOM_RELEASE="v0.0.5"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"

LOOM_DRIVER_AMD64_ASSET="driver-agent.linux-amd64"
LOOM_DRIVER_AMD64_SHA256="9dd94809801ff71d3e4c26581d48d44796c8e8be28be116b44d02cbd9fcb946c"
LOOM_DRIVER_ARM64_ASSET="driver-agent.linux-arm64"
LOOM_DRIVER_ARM64_SHA256="1c0a60bfb677a55159dea145dc46ead489b442d2cc55403dd451f3fadec4c7b5"

LOOM_SLAVE_AMD64_ASSET="slave-agent.linux-amd64"
LOOM_SLAVE_AMD64_SHA256="ce7d0b552a2ee880ef288d14c0d399630b961592fc73e78e98cece7a824ea965"
LOOM_SLAVE_ARM64_ASSET="slave-agent.linux-arm64"
LOOM_SLAVE_ARM64_SHA256="f7b0740cfb9d9a2c6fa1ad5f015b18c7ee4b3f618fe7082bb00bb828dc683ee6"

LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="4466b0342eaa90284dc4de0f0c03e6d08dbe02e4c12d0da6e7cb433c61ea1a0c"
SUPERPOWER_SKILLS_ASSET="driver-superpower-skills.tar.gz"
LOOM_DRIVER_CODEX_PROMPTS_ASSET="driver-codex-prompts.tar.gz"

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

loom_asset_for_arch() {
  local kind arch
  kind="$1"
  arch="$2"
  case "$kind:$arch" in
    driver:amd64) echo "$LOOM_DRIVER_AMD64_ASSET $LOOM_DRIVER_AMD64_SHA256" ;;
    driver:arm64) echo "$LOOM_DRIVER_ARM64_ASSET $LOOM_DRIVER_ARM64_SHA256" ;;
    slave:amd64)  echo "$LOOM_SLAVE_AMD64_ASSET $LOOM_SLAVE_AMD64_SHA256" ;;
    slave:arm64)  echo "$LOOM_SLAVE_ARM64_ASSET $LOOM_SLAVE_ARM64_SHA256" ;;
    *)
      echo "ERROR: unsupported loom asset kind/arch: $kind/$arch" >&2
      return 2
      ;;
  esac
}
