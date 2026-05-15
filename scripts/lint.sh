#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

GOLANGCI_VERSION="v1.62.2"
BIN_DIR=".bin"
LINT="$BIN_DIR/golangci-lint"

mkdir -p "$BIN_DIR"
if [[ ! -x "$LINT" ]]; then
    echo "Installing golangci-lint $GOLANGCI_VERSION into $BIN_DIR..."
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b "$BIN_DIR" "$GOLANGCI_VERSION"
fi

exec "$LINT" run ./...
