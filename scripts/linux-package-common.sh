LOOM_RELEASE="v0.0.10"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"

LOOM_DRIVER_AMD64_ASSET="driver-agent.linux-amd64"
LOOM_DRIVER_AMD64_SHA256="cc9467cde06f32b9dddb87946192bbf5f38f95008d5023836e3076af89734fd3"
LOOM_DRIVER_ARM64_ASSET="driver-agent.linux-arm64"
LOOM_DRIVER_ARM64_SHA256="15117e8a0326da2ec6a308e2836108f3be498615764a193c4ba6235ecac7d671"

LOOM_SLAVE_AMD64_ASSET="slave-agent.linux-amd64"
LOOM_SLAVE_AMD64_SHA256="67e6e79144e9e2c3cefdc7d3c0cbd67ebd87c55bd4116fe30b8d939412042910"
LOOM_SLAVE_ARM64_ASSET="slave-agent.linux-arm64"
LOOM_SLAVE_ARM64_SHA256="fad2c9ea341ad55283638da39dbe0ba4b17a941412d4af8e9aa1a09fd1fcb175"

LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="f9641c17e0a5105b4f97adf9ce70e186ee849fc4f03ad13fe3460cb54ec02ba9"
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
  if ! curl --fail --location --retry 2 --retry-all-errors --retry-delay 2 --output "$cache.part" "$url"; then
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
