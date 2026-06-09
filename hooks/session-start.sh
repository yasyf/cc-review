#!/usr/bin/env bash
# SessionStart hook: ensure the binary is built, then record the session's facts
# (best-effort — does nothing if the daemon isn't up). Reads the hook JSON on
# stdin and passes it through to `cc-review session-record`.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

[ -x "$BIN" ] || bash "$ROOT/scripts/install-binary.sh" >/dev/null 2>&1 || true
[ -x "$BIN" ] && exec "$BIN" session-record
exit 0
