#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GEN_DIR="$ROOT_DIR/integration-test/.generated/buckit-url"
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
    exit 1
    ;;
esac

mkdir -p "$GEN_DIR"

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
if [[ -z "$rpm_url" || "$rpm_url" == "null" ]]; then
  echo "Could not find a Buckit RPM asset ending with $rpm_suffix in $release_api_url" >&2
  exit 1
fi

printf '%s\n' "$rpm_url"
