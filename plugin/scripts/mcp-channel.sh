#!/usr/bin/env bash
# MCP entrypoint for the opt-in channel server. Pre-warm the version-exact
# binary so binrun's resolve/download noise stays off the MCP stdio transport
# (stdout must be clean JSON-RPC), then exec it from the warm cache. bin/cc-review
# is the committed binrun shim; it resolves and caches the pinned artifact.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

"$BIN" --version >/dev/null 2>&1 || true
exec "$BIN" mcp-channel
