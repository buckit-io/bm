#!/bin/sh
# install.sh — per-user installer for bm (Buckit Manager).
#
# Downloads the latest stable bm binary for this OS/arch, verifies its
# SHA-256 against the published release pointer, and installs it to a
# per-user bin directory. No sudo, no system-wide install — bm is a
# personal-tool binary.
#
# Usage:
#   curl -fsSL https://buckit-io.github.io/bm/install.sh | sh
#
# Environment overrides:
#   BM_INSTALL_DIR   install directory (default: $HOME/.local/bin)
#   BM_PAGES_BASE    gh-pages base URL (default: https://buckit-io.github.io/bm)
#   BM_RELEASE_BASE  release download base (default: https://github.com/buckit-io/bm/releases/download)

set -eu

PAGES_BASE="${BM_PAGES_BASE:-https://buckit-io.github.io/bm}"
RELEASE_BASE="${BM_RELEASE_BASE:-https://github.com/buckit-io/bm/releases/download}"
INSTALL_DIR="${BM_INSTALL_DIR:-$HOME/.local/bin}"

# Minisign public key for optional signature verification (best-effort;
# the SHA-256 check below is the hard gate). Matches internal/update.
MINISIGN_PUBKEY="RWQx2s09Ko3rYmr64TBI992Oppjy00FUMQAUM9Ezf9czdMFOhB7CmKKU"

err() {
	echo "install.sh: $*" >&2
	exit 1
}

info() {
	echo "==> $*"
}

# detect_platform sets PLATFORM to one of the published target tuples.
detect_platform() {
	os="$(uname -s)"
	arch="$(uname -m)"

	case "$os" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) err "unsupported OS '$os'. Windows users: run install.ps1. Other: download a binary from $RELEASE_BASE" ;;
	esac

	case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) err "unsupported architecture '$arch'. Supported: amd64, arm64." ;;
	esac

	PLATFORM="${os}-${arch}"
}

# fetch URL -> stdout
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		err "need curl or wget to download bm"
	fi
}

# fetch_to URL FILE
fetch_to() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		err "need curl or wget to download bm"
	fi
}

# sha256_of FILE -> hex digest on stdout
sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		err "need sha256sum or shasum to verify the download"
	fi
}

main() {
	detect_platform
	info "platform: $PLATFORM"

	pointer_url="$PAGES_BASE/manager/bm/release/$PLATFORM/bm.sha256sum"
	info "resolving latest release"
	pointer="$(fetch "$pointer_url")" || err "could not fetch release pointer at $pointer_url"

	# Pointer format: "<sha256>  bm.<tag>"
	want_sha="$(echo "$pointer" | awk '{print $1}')"
	name="$(echo "$pointer" | awk '{print $2}')"
	tag="${name#bm.}"
	case "$tag" in
	RELEASE.*) ;;
	*) err "unexpected release pointer payload: $pointer" ;;
	esac
	[ -n "$want_sha" ] || err "release pointer missing checksum"
	info "latest stable: $tag"

	asset="bm-$PLATFORM.$tag"
	download_url="$RELEASE_BASE/$tag/$asset"

	tmp="$(mktemp -d "${TMPDIR:-/tmp}/bm-install.XXXXXX")"
	trap 'rm -rf "$tmp"' EXIT
	bin="$tmp/bm"

	info "downloading $asset"
	fetch_to "$download_url" "$bin" || err "download failed: $download_url"

	got_sha="$(sha256_of "$bin")"
	if [ "$got_sha" != "$want_sha" ]; then
		err "checksum mismatch (expected $want_sha, got $got_sha) — refusing to install"
	fi
	info "sha256 verified"

	# Optional minisign verification when the tool is available.
	if command -v minisign >/dev/null 2>&1; then
		if fetch_to "$download_url.minisig" "$bin.minisig" 2>/dev/null; then
			if minisign -V -P "$MINISIGN_PUBKEY" -m "$bin" -x "$bin.minisig" >/dev/null 2>&1; then
				info "minisign signature verified"
			else
				err "minisign signature verification failed — refusing to install"
			fi
		fi
	fi

	mkdir -p "$INSTALL_DIR"
	dest="$INSTALL_DIR/bm"
	chmod 0755 "$bin"
	mv -f "$bin" "$dest"

	# macOS: clear the quarantine attribute so Gatekeeper doesn't block
	# the unsigned binary on first run.
	if [ "$(uname -s)" = "Darwin" ] && command -v xattr >/dev/null 2>&1; then
		xattr -d com.apple.quarantine "$dest" 2>/dev/null || true
	fi

	info "installed bm to $dest"

	echo
	"$dest" version || true

	# PATH hint.
	path_hint_printed=0
	case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		path_hint_printed=1
		echo
		echo "To start the Buckit Manager, complete these two steps:"
		echo
		echo "Step 1: Add $INSTALL_DIR to your PATH"
		shell_name="${SHELL:-}"
		case "${shell_name##*/}" in
		zsh)
			echo "Run:"
			echo
			echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc"
			;;
		bash)
			echo "Run:"
			echo
			echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
			;;
		fish)
			echo "Run:"
			echo
			echo "  mkdir -p ~/.config/fish && echo 'fish_add_path \"$INSTALL_DIR\"' >> ~/.config/fish/config.fish && source ~/.config/fish/config.fish"
			;;
		*)
			echo "For POSIX-style shells, add it, e.g.:"
			echo
			echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
			;;
		esac
		;;
	esac

	echo
	if [ "$path_hint_printed" -eq 1 ]; then
		echo "Step 2: Start the Buckit Manager"
		echo "Run:"
		echo "  bm web"
	else
		echo "Start the Buckit Manager"
		echo "Run:"
		echo "  bm web"
	fi
}

main "$@"
