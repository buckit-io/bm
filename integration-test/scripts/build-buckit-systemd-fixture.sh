#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GEN_DIR="$ROOT_DIR/integration-test/.generated/buckit-systemd"
IMAGE_TAG="${BM_E2E_REPLACEMENT_IMAGE:-bm-e2e-buckit-systemd:local}"
RELEASE_TAG="${BM_E2E_BUCKIT_RELEASE_TAG:-}"
RELEASE_API_BASE="https://api.github.com/repos/buckit-io/buckit/releases"

arch_raw="${BM_E2E_BUCKIT_ARCH:-}"
if [[ -z "$arch_raw" ]]; then
  arch_raw="$(docker version -f '{{.Server.Arch}}' 2>/dev/null || uname -m)"
fi
arch_raw="$(printf '%s' "$arch_raw" | tr -d '[:space:]')"

case "$arch_raw" in
  amd64 | x86_64)
    rpm_suffix="x86_64.rpm"
    ;;
  arm64 | aarch64)
    rpm_suffix="aarch64.rpm"
    ;;
  *)
    echo "Unsupported Buckit fixture architecture: $arch_raw" >&2
    echo "Set BM_E2E_BUCKIT_ARCH to one of: amd64, x86_64, arm64, aarch64." >&2
    exit 1
    ;;
esac

verify_checksum() {
  local checksum_file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$GEN_DIR" && sha256sum -c "$checksum_file")
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    (cd "$GEN_DIR" && shasum -a 256 -c "$checksum_file")
    return
  fi
  echo "Neither sha256sum nor shasum is available for checksum verification." >&2
  exit 1
}

mkdir -p "$GEN_DIR"
rm -f "$GEN_DIR"/*

release_api_url="${RELEASE_API_BASE}/latest"
if [[ -n "$RELEASE_TAG" ]]; then
  release_api_url="${RELEASE_API_BASE}/tags/${RELEASE_TAG}"
fi

release_json="$GEN_DIR/release.json"
curl -fsSL \
  -H 'Accept: application/vnd.github+json' \
  "$release_api_url" \
  -o "$release_json"

rpm_url="${BM_E2E_BUCKIT_RPM_URL:-$(jq -r --arg suffix "$rpm_suffix" '.assets[] | select(.name | endswith($suffix)) | .browser_download_url' "$release_json" | head -n1)}"
sha_url="$(jq -r --arg suffix "${rpm_suffix}.sha256sum" '.assets[] | select(.name | endswith($suffix)) | .browser_download_url' "$release_json" | head -n1)"

if [[ -z "$rpm_url" || "$rpm_url" == "null" ]]; then
  echo "Could not find a Buckit RPM asset ending with $rpm_suffix in $release_api_url" >&2
  exit 1
fi
if [[ -z "$sha_url" || "$sha_url" == "null" ]]; then
  echo "Could not find a Buckit checksum asset ending with ${rpm_suffix}.sha256sum in $release_api_url" >&2
  exit 1
fi

rpm_name="$(basename "$rpm_url")"
sha_name="$(basename "$sha_url")"

echo "Downloading Buckit replacement fixture artifacts:"
echo "  RPM: $rpm_url"
echo "  SHA: $sha_url"

curl -fsSL "$rpm_url" -o "$GEN_DIR/$rpm_name"
curl -fsSL "$sha_url" -o "$GEN_DIR/$sha_name"
sed -i.bak "s#  dist/#  #g" "$GEN_DIR/$sha_name"
rm -f "$GEN_DIR/$sha_name.bak"
verify_checksum "$sha_name"

docker build \
  -f "$ROOT_DIR/integration-test/buckit-systemd/Dockerfile" \
  --build-arg "BUCKIT_RPM_FILENAME=$rpm_name" \
  -t "$IMAGE_TAG" \
  "$ROOT_DIR/integration-test"
