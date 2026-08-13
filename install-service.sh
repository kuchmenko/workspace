#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo "$0" "$@"
  fi
  echo "Run this installer as root or install sudo." >&2
  exit 1
fi

case "$(uname -s)" in Linux) ;; *) echo "Debian Linux is required." >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Unsupported architecture." >&2; exit 1 ;; esac

repo=https://github.com/kuchmenko/workspace
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

latest_headers=$(curl -fsSIL "$repo/releases/latest")
tag=$(printf '%s\n' "$latest_headers" | sed -n 's|^[Ll]ocation: .*/tag/\([^[:space:]]*\).*|\1|p' | tail -n 1 | tr -d '\r')
if [ -z "$tag" ]; then echo "Could not resolve the latest public release." >&2; exit 1; fi
asset="ws-linux-$arch.tar.gz"
base="$repo/releases/download/$tag"
curl -fL "$base/$asset" -o "$tmp/$asset"
curl -fL "$base/checksums.txt" -o "$tmp/checksums.txt"
(cd "$tmp" && grep "  $asset\$" checksums.txt | sha256sum -c -)
tar xzf "$tmp/$asset" -C "$tmp"
chmod 0755 "$tmp/ws-linux-$arch"
echo "Release checksums verify transfer integrity; release assets are not cryptographically signed."
exec "$tmp/ws-linux-$arch" sync service install "$@"
