#!/bin/sh
# One-line installer for the tpt-cortex native tools (cortex-daemon +
# cortex-engine). Usage:
#
#   curl -fsSL https://github.com/tpt-solutions/tpt-pwa/releases/latest/download/install.sh | sh
#
# or, pinned to a version:
#   TPT_VERSION=v0.2.0 sh install.sh
#
# Installs into ~/.tpt/bin by default (override with TPT_INSTALL_DIR), adds
# itself to PATH guidance at the end, and verifies every download against the
# published SHA256SUMS.
# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
set -eu

REPO="tpt-solutions/tpt-pwa"
VERSION="${TPT_VERSION:-latest}"
INSTALL_DIR="${TPT_INSTALL_DIR:-$HOME/.tpt/bin}"

os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux) os_name="linux" ;;
  Darwin) os_name="macos" ;;
  *) echo "install.sh: unsupported OS '$os' (see scripts/install.ps1 for Windows)" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch_name="amd64" ;;
  aarch64|arm64) arch_name="arm64" ;;
  *) echo "install.sh: unsupported architecture '$arch'" >&2; exit 1 ;;
esac
TARGET="${os_name}-${arch_name}"

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
  if [ -z "$VERSION" ]; then
    echo "install.sh: could not determine the latest release" >&2
    exit 1
  fi
fi
echo "installing tpt-cortex $VERSION for $TARGET -> $INSTALL_DIR"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BASE="https://github.com/$REPO/releases/download/$VERSION"

curl -fSsL -o "$TMP/SHA256SUMS.txt" "$BASE/SHA256SUMS-$TARGET.txt"
ARCHIVE="tpt-cortex-$TARGET.tar.gz"
curl -fSsL -o "$TMP/$ARCHIVE" "$BASE/$ARCHIVE"

(cd "$TMP" && sha256sum -c SHA256SUMS.txt) || {
  echo "install.sh: checksum verification FAILED -- refusing to install" >&2
  exit 1
}

mkdir -p "$INSTALL_DIR"
tar xzf "$TMP/$ARCHIVE" -C "$TMP"
mv "$TMP/cortex-daemon" "$TMP/cortex-engine" "$INSTALL_DIR/"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo
    echo "Add $INSTALL_DIR to your PATH, e.g. in ~/.zshrc or ~/.bashrc:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
echo
echo "Verify the installation:"
echo "  $INSTALL_DIR/cortex-daemon doctor"
echo "  $INSTALL_DIR/cortex-engine"
