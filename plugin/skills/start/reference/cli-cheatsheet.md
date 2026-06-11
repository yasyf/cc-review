# CLI cheatsheet

Every command is a thin call to the local daemon, which lazy-starts on first use. The skill always passes `--session "$CLAUDE_CODE_SESSION_ID"` and `--cwd "$PWD"`; the daemon resolves the review from the session id and repo root, so you never pass a review id.

| Command | What it does |
| --- | --- |
| `cc-review start --session <id> --cwd <dir> [--new] [--base <ref>]` | Snapshot the working tree and print the review URL, a `channel: active\|inactive` line, a `setup: {"offer":…,"reason":…}` line (the first-run channel-approval offer), and — only when a new version was created — an `organize: <AI request JSON>` line carrying the daemon's eager organize request (omitted on an unchanged resume). By default it resumes this window's open review (following `/clear`/resume rotation; an orphaned review from an exited window is adopted), and `--new` forces a fresh one. A new review pins its diff base to HEAD when the working tree is dirty, to the trunk fork point when clean so the whole branch is reviewed, or to `--base <ref>`. Later snapshots diff against the pin; with nothing to review, start exits non-zero saying `no changes to review`. |
| `cc-review watch --session <id> --cwd <dir>` | Print one JSON event per line (events you originate — your replies and MCP tool calls — are filtered out) and exit on `submit`. Run it under a persistent Monitor. |
| `cc-review reply --comment <id> --kind <question\|ask\|clarification> --body <text> [--header <chip>] [--multi-select] [--options-json <json>]` | Post a reply under a comment. Returns immediately. `--kind ask` requires `--body` and `--options-json`, a JSON array of `{label, description?, preview?}`; `--header` is a short chip like "Approach"; `--multi-select` allows multiple picks. |
| `cc-review reply --answer-to <replyId> --select <label> [--select <label>] [--other <text>] [--notes <note>]` \| `--answer <text>` | Record the human's answer during the post-submit drain: `--select`/`--other`/`--notes` for an `ask` target, `--answer` only for a plain `question` target. |
| `cc-review feedback --session <id> --cwd <dir>` | Print the frozen feedback JSON (`threads` + `open_questions`) after Submit. |
| `cc-review status [--session <id> --cwd <dir>]` | Show daemon and review status. |
| `cc-review stop` | Stop the background daemon. |
| `cc-review setup-channels [--check\|--apply\|--decline]` | Approve cc-review's channel. The skill reads the offer from `start`'s `setup:` line, not from `--check` (which prints the same `{offer,reason}` and remains for manual use); `--apply` writes the approved-channels config (admin prompt); `--decline` records a no. See `channels-setup.md`. |

The daemon keeps state under `~/.cc-review` (sqlite db, per-review patch + feedback files, the control socket, and the HTTP handshake). It is never relocated by an environment variable.
