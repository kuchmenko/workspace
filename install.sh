#!/bin/sh
set -eu

REPO="kuchmenko/workspace"
BINARY="ws"
INSTALL_DIR="${WS_INSTALL_DIR:-$HOME/.local/bin}"

if ! command -v gh >/dev/null 2>&1; then
  echo "GitHub CLI is required. Install gh, then run: gh auth login" >&2
  exit 1
fi

if ! gh auth status --hostname github.com >/dev/null 2>&1; then
  echo "GitHub authentication is required. Run: gh auth login" >&2
  exit 1
fi

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

# Get latest private release tag from GitHub
if ! TAG="$(gh api "repos/$REPO/releases/latest" --jq .tag_name 2>/dev/null)" || [ -z "$TAG" ]; then
  echo "Cannot access $REPO releases. Check gh authentication and repository access." >&2
  exit 1
fi

ASSET="ws-${OS}-${ARCH}.tar.gz"

echo "Installing $BINARY $TAG ($OS/$ARCH)..."

LEGACY_UNIT="$HOME/.config/systemd/user/ws-daemon.service"
if [ "$OS" = "linux" ] && { [ -e "$LEGACY_UNIT" ] || { command -v systemctl >/dev/null 2>&1 && systemctl --user cat ws-daemon.service >/dev/null 2>&1; }; }; then
  CLEANUP_FAILED=0
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user disable --now ws-daemon.service >/dev/null 2>&1 || CLEANUP_FAILED=1
  else
    CLEANUP_FAILED=1
  fi
  rm -f "$LEGACY_UNIT" || CLEANUP_FAILED=1
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload >/dev/null 2>&1 || CLEANUP_FAILED=1
  fi
  if [ "$CLEANUP_FAILED" -ne 0 ]; then
    echo "Warning: could not fully retire ws-daemon.service. Before running 'ws sync', run: systemctl --user disable --now ws-daemon.service; rm -f '$LEGACY_UNIT'; systemctl --user daemon-reload" >&2
  else
    echo "Removed legacy ws-daemon.service"
  fi
fi

# Download and extract
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if ! gh release download "$TAG" --repo "$REPO" --pattern "$ASSET" --dir "$TMP" 2>/dev/null; then
  echo "Cannot download $ASSET from $REPO release $TAG. Check repository access and release assets." >&2
  exit 1
fi
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
