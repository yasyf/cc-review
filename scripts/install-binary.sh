#!/usr/bin/env bash
# Build bin/cc-review from source if it is missing. The plugin ships the source,
# not a platform binary, so the binary is built once on first use. Requires `go`
# and `bun` on PATH.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

if [ -x "$BIN" ]; then
  exit 0
fi

command -v go >/dev/null 2>&1 || { echo "cc-review: 'go' is required to build the binary" >&2; exit 1; }
command -v bun >/dev/null 2>&1 || { echo "cc-review: 'bun' is required to build the web UI" >&2; exit 1; }

echo "cc-review: building the web UI and binary (first run)..." >&2
( cd "$ROOT/web" && bun install --frozen-lockfile && bunx vite build )

VERSION="$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)"
( cd "$ROOT" && go build -ldflags "-X github.com/yasyf/cc-review/internal/version.Version=${VERSION}" -o "$BIN" ./cmd/cc-review )

echo "cc-review: built $BIN" >&2
