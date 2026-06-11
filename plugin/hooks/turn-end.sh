#!/usr/bin/env bash
# Stop hook: close the open turn with a post-edit working-tree snapshot.
# Always exits 0 and prints nothing.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

[ -x "$BIN" ] || exit 0
exec "$BIN" turn-end
