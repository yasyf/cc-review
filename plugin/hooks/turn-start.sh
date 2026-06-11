#!/usr/bin/env bash
# UserPromptSubmit hook: open a turn with a pre-edit working-tree snapshot.
# Always exits 0 and prints nothing — UserPromptSubmit stdout is injected
# into Claude's context.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

[ -x "$BIN" ] || exit 0
exec "$BIN" turn-start
