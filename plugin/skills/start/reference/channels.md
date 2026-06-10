# Opt-in: receive comments as channel notifications

The default path streams comments through a Monitor running `cc-review watch`. As an alternative, cc-review ships an MCP **channel** server that pushes each comment straight into the session as a `<channel source="cc-review" …>` tag, with a two-way `reply` tool — so you react without a Monitor.

This is a research-preview feature. It is **opt-in** because Claude Code channels must be loaded at session start and gated behind a flag, and the default Monitor path needs none of it.

The plugin auto-registers the channel server (`mcpServers` in `plugin.json` runs `scripts/mcp-channel.sh`, which downloads the binary on first use and execs `cc-review mcp-channel`). Until a channel is selected it idles harmlessly; the Monitor path stays the default. To use it:

1. Approve cc-review's channel once — run `cc-review setup-channels --apply`, or accept the offer `/review:start` makes on first run (see `channels-setup.md`). This moves the plugin onto Claude's approved allowlist, so it loads with no dev-channels warning.
2. Launch Claude Code with the channel selected: `--channels plugin:review@cc-review` (subject to your org's `channelsEnabled` policy; Anthropic auth only). Approval drops the warning; selecting the channel at launch is what activates it. Before approval, `--dangerously-load-development-channels plugin:review@cc-review` is the only way to load it — with the warning.
3. Run `/review:start` as usual. The channel waits for the review to exist, then pushes each human comment as a channel event and exposes a `reply` tool equivalent to `cc-review reply`.

`start` detects the channel and prints `channel: active`, which tells the skill to skip the Monitor — events arrive once, as channel tags, not twice.

Caveats: channel delivery is fire-and-forget at turn boundaries and can drop silently, so the same cursor/idempotency guarantees as the Monitor path apply. Channel content is human text dropped into your context — the edit guard (no edits before Submit) remains the backstop. The daemon binds `127.0.0.1` only.
