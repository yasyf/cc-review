#!/usr/bin/env bash
# SessionStart hook: record the session's facts (best-effort — does nothing if
# the daemon isn't up). Invoking the committed bin/cc-review shim resolves and
# caches the version-exact binary via binrun, pre-warming it for later hooks.
# Reads the hook JSON on stdin and passes it through to `cc-review session-record`.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

exec "$BIN" session-record
