LOOM_RELEASE="v0.0.8"
LOOM_BASE_URL="https://github.com/agentserver/loom/releases/download/$LOOM_RELEASE"

LOOM_DRIVER_AMD64_ASSET="driver-agent.linux-amd64"
LOOM_DRIVER_AMD64_SHA256="12016639c3b7b54156384fd3050c730341eb657ed95ab4d6463da71aebc8afe1"
LOOM_DRIVER_ARM64_ASSET="driver-agent.linux-arm64"
LOOM_DRIVER_ARM64_SHA256="78b653f3cc42a7bc55c3f65caf8b143ac49a402720086e1a464fde9966fdac51"

LOOM_SLAVE_AMD64_ASSET="slave-agent.linux-amd64"
LOOM_SLAVE_AMD64_SHA256="01b8bb4064fd938a4165ade7cab67d0f0f608336d86c9207a01b3c3b8a5b37c1"
LOOM_SLAVE_ARM64_ASSET="slave-agent.linux-arm64"
LOOM_SLAVE_ARM64_SHA256="ed21d2c8b38c2169de959096691b9a5f793347bfe2793468ce994474887e10c6"

LOOM_DRIVER_SKILLS_ASSET="driver-skills.tar.gz"
LOOM_DRIVER_SKILLS_SHA256="e8cbab1188f5368c5a8fb0492807f5a5f2526ec98e71860911e8beecf6718926"
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
