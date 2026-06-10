#!/usr/bin/env bash
# MCP entrypoint for the opt-in channel server: download the binary on first
# use, then exec it. stdout is the MCP stdio transport, so download output goes
# to stderr.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/cc-review"

[ -x "$BIN" ] || bash "$ROOT/scripts/install-binary.sh" 1>&2
exec "$BIN" mcp-channel
