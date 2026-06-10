#!/usr/bin/env bash
# Download the prebuilt cc-review binary for this platform from the GitHub
# release matching the plugin version. The plugin payload is self-contained
# (no Go/web source ships), so the binary always comes from release assets.
# A stale release binary is replaced when the plugin version moves; local dev
# builds (anything not reporting vX.Y.Z) are left alone.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$ROOT/.claude-plugin/plugin.json")"

if [ -x "$BIN" ]; then
  # Release builds print exactly the tag (release.yml injects only
  # version.Version=$GITHUB_REF_NAME) — keep that coupling or this re-downloads
  # every run.
  installed="$("$BIN" --version 2>/dev/null || true)"
  case "$installed" in
    "v$VERSION") exit 0 ;;
    v[0-9]*) ;;
    *) exit 0 ;;
  esac
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
esac
URL="https://github.com/yasyf/cc-review/releases/download/v${VERSION}/cc-review_${OS}_${ARCH}"

echo "cc-review: downloading ${URL}" >&2
mkdir -p "$ROOT/bin"
# Stage in bin/ (same filesystem) and rename: writing onto a running executable
# fails with ETXTBSY on Linux, and rename keeps the old inode alive for any
# daemon still executing it.
TMP="$(mktemp "$ROOT/bin/.cc-review.XXXXXX")"
trap 'rm -f "$TMP"' EXIT
curl -fsSL --retry 2 -o "$TMP" "$URL"
chmod +x "$TMP"
mv -f "$TMP" "$BIN"
echo "cc-review: installed $BIN" >&2
