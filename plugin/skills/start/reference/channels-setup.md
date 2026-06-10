# Silencing the dev-channels warning

Launching Claude with `--dangerously-load-development-channels plugin:review@cc-review` shows a **"WARNING: Loading development channels"** confirmation on every start. That dialog exists only for the development path. The same channel loads with no dialog once cc-review is on Claude's *approved* allowlist and is selected through the normal channels mechanism.

`cc-review setup-channels` makes that switch. The `/review:start` skill runs it once, the first time a development-channels session has no approval yet.

## What `--apply` writes

1. **`managed-settings.json`** (the only file Claude reads `allowedChannelPlugins` from): sets `channelsEnabled: true` and adds `{ "marketplace": "cc-review", "plugin": "review" }`. This file is machine-wide and root-owned, so the write goes through a macOS admin-password prompt (`osascript … with administrator privileges`). Other keys in the file are preserved.
2. **`~/.claude/settings.json`** (honoring `CLAUDE_CONFIG_DIR`): records `plugin:review@cc-review` in the `channels` array as the selection. Other keys are preserved. (Current Claude builds still require `--channels` at launch to activate the channel; this key alone does not auto-load it.)

It then writes a marker at `~/.cc-review/channels-setup.json` so the offer is never made again.

## After applying

Relaunch with `--channels plugin:review@cc-review` in place of `--dangerously-load-development-channels plugin:review@cc-review` (in your alias, `ccp run`, or however you start Claude). Approval moves the plugin onto the allowlist, so the approved `--channels` flag loads it with no warning; you still select the channel at launch — approval does not auto-load it. The `--dangerously-…` flag is the sole trigger for the dialog, so swapping it out is what removes the warning.

## Gating

`--check` reports `{ "offer": true }` only when all three hold: no marker yet, cc-review is not already approved, and the current session descends from a Claude launched with the development-channels flag (so Monitor-only users are never asked). `--decline` records a no without writing any settings.

## Undo

Delete the `channels` entry from `~/.claude/settings.json` and the cc-review entry (and, if you added nothing else, `channelsEnabled`) from `managed-settings.json`. Remove `~/.cc-review/channels-setup.json` to be offered again.
