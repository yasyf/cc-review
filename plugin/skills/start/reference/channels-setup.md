# Approving the cc-review channel

The channel loads with no dialog only once cc-review is on Claude's *approved* allowlist and is selected through the normal channels mechanism (`--channels plugin:cc-review@cc-review`). Before approval, `--dangerously-load-development-channels plugin:cc-review@cc-review` is the only way to load it, and it shows a **"WARNING: Loading development channels"** confirmation on every start.

`cc-review setup-channels` grants that approval. `cc-review start` runs the same gating check and prints the result as its `setup: {"offer":…,"reason":…}` line; the `/cc-review:start` skill makes the offer from that line once, the first time a session has no approval yet.

## What `--apply` writes

1. **`managed-settings.json`** (the only file Claude reads `allowedChannelPlugins` from): sets `channelsEnabled: true` and adds `{ "marketplace": "cc-review", "plugin": "cc-review" }`. This file is machine-wide and root-owned, so the write goes through a macOS admin-password prompt (`osascript … with administrator privileges`). Other keys in the file are preserved.
2. **`~/.claude/settings.json`** (honoring `CLAUDE_CONFIG_DIR`): records `plugin:cc-review@cc-review` in the `channels` array as the selection. Other keys are preserved. (Current Claude builds still require `--channels` at launch to activate the channel; this key alone does not auto-load it.)

It then writes a marker at `~/.cc-review/channels-setup.json` so the offer is never made again.

## After applying

Launch with `--channels plugin:cc-review@cc-review`, replacing `--dangerously-load-development-channels plugin:cc-review@cc-review` if you used it (in your alias, `ccp run`, or however you start Claude). Approval moves the plugin onto the allowlist, so the approved `--channels` flag loads it with no warning; you still select the channel at launch — approval does not auto-load it. The `--dangerously-…` flag is the sole trigger for the dialog, so swapping it out is what removes the warning.

## Gating

`start`'s `setup:` line (and `--check`, which runs the same check) reports `{ "offer": true }` only when both hold: no marker yet, and cc-review is not already approved. `--decline` records a no without writing any settings.

## Undo

Delete the `channels` entry from `~/.claude/settings.json` and the cc-review entry (and, if you added nothing else, `channelsEnabled`) from `managed-settings.json`. Remove `~/.cc-review/channels-setup.json` to be offered again.
