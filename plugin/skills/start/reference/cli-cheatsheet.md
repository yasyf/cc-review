# CLI cheatsheet

Every command is a thin call to the local daemon, which lazy-starts on first use. The skill always passes `--session "$CLAUDE_CODE_SESSION_ID"` and `--cwd "$PWD"`; the daemon resolves the review from the session id and repo root, so you never pass a review id.

| Command | What it does |
| --- | --- |
| `cc-review start --session <id> --cwd <dir> [--new]` | Snapshot the working tree and print the review URL plus a `channel: active\|inactive` line. By default it resumes the repo's open review — adopting it across session rotation — and `--new` forces a fresh one. |
| `cc-review watch --session <id> --cwd <dir>` | Print one JSON event per line (excluding your own replies) and exit on `submit`. Run it under a persistent Monitor. |
| `cc-review reply --comment <id> --kind <question\|ask\|clarification> --body <text> [--header <chip>] [--multi-select] [--options-json <json>]` | Post a reply under a comment. Returns immediately. `--kind ask` requires `--body` and `--options-json`, a JSON array of `{label, description?, preview?}`; `--header` is a short chip like "Approach"; `--multi-select` allows multiple picks. |
| `cc-review reply --answer-to <replyId> --select <label> [--select <label>] [--other <text>] [--notes <note>]` \| `--answer <text>` | Record the human's answer during the post-submit drain: `--select`/`--other`/`--notes` for an `ask` target, `--answer` only for a plain `question` target. |
| `cc-review feedback --session <id> --cwd <dir>` | Print the frozen feedback JSON (`threads` + `open_questions`) after Submit. |
| `cc-review status [--session <id> --cwd <dir>]` | Show daemon and review status. |
| `cc-review stop` | Stop the background daemon. |
| `cc-review setup-channels [--check\|--apply\|--decline]` | First-run offer to approve cc-review's channel. `--check` prints `{offer,reason}`; `--apply` writes the approved-channels config (admin prompt); `--decline` records a no. See `channels-setup.md`. |

The daemon keeps state under `~/.cc-review` (sqlite db, per-review patch + feedback files, the control socket, and the HTTP handshake). It is never relocated by an environment variable.
