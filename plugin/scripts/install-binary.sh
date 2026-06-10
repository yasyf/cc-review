#!/usr/bin/env bash
# Download the prebuilt cc-review binary for this platform from the GitHub
# release matching the plugin version. The plugin payload is self-contained
# (no Go/web source ships), so the binary always comes from release assets.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

if [ -x "$BIN" ]; then
  exit 0
fi

VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$ROOT/.claude-plugin/plugin.json")"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
esac
URL="https://github.com/yasyf/cc-review/releases/download/v${VERSION}/cc-review_${OS}_${ARCH}"

echo "cc-review: downloading ${URL}" >&2
mkdir -p "$ROOT/bin"
curl -fsSL --retry 2 -o "$BIN" "$URL"
chmod +x "$BIN"
echo "cc-review: installed $BIN" >&2
