# Opt-in: receive review events as channel notifications

The default path streams review events through a Monitor running `cc-review watch`. As an alternative, cc-review ships an MCP **channel** server that pushes each review event straight into the session as a `<channel source="cc-review" …>` tag, with two-way MCP tools — so you react without a Monitor.

This is a research-preview feature. It is **opt-in** because Claude Code channels must be loaded at session start and gated behind a flag, and the default Monitor path needs none of it.

The plugin auto-registers the channel server (`mcpServers` in `plugin.json` runs `scripts/mcp-channel.sh`, which downloads the binary on first use and execs `cc-review mcp-channel`). The server polls and attaches to the review regardless of whether channels are live — Claude Code gives it no availability signal and silently drops its notifications when the channel isn't selected — so the Monitor path stays the default until the window is proven. To use it:

1. Approve cc-review's channel once — run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" setup-channels --apply`, or accept the offer `/cc-review:start` makes on first run (see `channels-setup.md`). This moves the plugin onto Claude's approved allowlist, so it loads with no dev-channels warning.
2. Launch Claude Code with the channel selected: `--channels plugin:cc-review@cc-review` (subject to your org's `channelsEnabled` policy; Anthropic auth only). Approval drops the warning; selecting the channel at launch is what activates it. Before approval, `--dangerously-load-development-channels plugin:cc-review@cc-review` is the only way to load it — with the warning.
3. Run `/cc-review:start` as usual. The channel waits for the review to exist, then pushes each review event (comments, `ai.request.created`, `file.states`, `version.created`) as a channel tag and exposes five tools: `reply` (equivalent to `cc-review reply`), `set_file_states`, `update_ai_request`, `submit_organization`, `get_review_files`.

`start` prints a three-state `channel:` line as the second of its output lines (URL, `channel:`, `setup:`, and `organize:` on a new version): `pending` while the channel server is attached but this window is unproven, `active` only after a delivered `<channel>` tag was acknowledged via the hidden `cc-review channel-ack`, and `inactive` with no channel consumer. Only `active` tells the skill to skip the Monitor — events arrive once, as channel tags, not twice. On `pending` the Monitor stays the route, and the first delivered tag is what proves the window.

Caveats: channel delivery is fire-and-forget at turn boundaries and can drop silently, so the same cursor/idempotency guarantees as the Monitor path apply. Channel content is human text dropped into your context — the edit guard (no edits before Submit) remains the backstop. The daemon binds `127.0.0.1` only.
