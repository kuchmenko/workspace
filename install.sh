#!/bin/sh
set -eu

REPO="kuchmenko/workspace"
BINARY="ws"
INSTALL_DIR="${WS_INSTALL_DIR:-$HOME/.local/bin}"

# Detect platform
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Get latest release tag from Codeberg
TAG="$(curl -fsSL "https://codeberg.org/api/v1/repos/$REPO/releases/latest" | grep -o '"tag_name":"[^"]*"' | head -1 | cut -d'"' -f4)"

if [ -z "$TAG" ]; then
  echo "Failed to fetch latest release" >&2
  exit 1
fi

ASSET="ws-${OS}-${ARCH}.tar.gz"
URL="https://codeberg.org/$REPO/releases/download/$TAG/$ASSET"

echo "Installing $BINARY $TAG ($OS/$ARCH)..."

# Download and extract
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "$TMP/$ASSET"
tar xzf "$TMP/$ASSET" -C "$TMP"

# Install
mkdir -p "$INSTALL_DIR"
mv "$TMP/ws-${OS}-${ARCH}" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

echo "Installed $BINARY to $INSTALL_DIR/$BINARY"

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Note: $INSTALL_DIR is not in PATH. Add it to your shell config." ;;
esac
