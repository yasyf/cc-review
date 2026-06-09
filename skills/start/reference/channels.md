# Opt-in: receive comments as channel notifications

The default path streams comments through a Monitor running `cc-review watch`. As an alternative, cc-review ships an MCP **channel** server that pushes each comment straight into the session as a `<channel source="cc-review" …>` tag, with a two-way `reply` tool — so you react without a Monitor.

This is a research-preview feature. It is **opt-in** because Claude Code channels must be loaded at session start and gated behind a flag, and the default Monitor path needs none of it.

To use it:

1. Launch Claude Code with channels enabled (`--channels`, subject to your org's `channelsEnabled` policy; Anthropic auth only).
2. Register the channel MCP server so it loads at session start, pointing at the shipped binary:

   ```json
   {
     "mcpServers": {
       "cc-review": {
         "command": "${CLAUDE_PLUGIN_ROOT}/bin/cc-review",
         "args": ["mcp-channel"]
       }
     }
   }
   ```

3. Run `/review:start` as usual. The channel waits for the review to exist, then pushes each human comment as a channel event and exposes a `reply` tool equivalent to `cc-review reply`.

Caveats: channel delivery is fire-and-forget at turn boundaries and can drop silently, so the same cursor/idempotency guarantees as the Monitor path apply. Channel content is human text dropped into your context — the edit guard (no edits before Submit) remains the backstop. The daemon binds `127.0.0.1` only.
