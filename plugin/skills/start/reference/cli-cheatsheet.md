# CLI cheatsheet

Every command is a thin call to the local daemon, which lazy-starts on first use. The skill always passes `--session "$CLAUDE_CODE_SESSION_ID"` and `--cwd "$PWD"`; the daemon resolves the review from the session id and repo root, so you never pass a review id.

| Command | What it does |
| --- | --- |
| `cc-review start --session <id> --cwd <dir> [--resume] [--new]` | Snapshot the working tree and print the review URL. `--resume` adopts the latest open review for the repo when there's no session match; `--new` forces a fresh review. |
| `cc-review watch --session <id> --cwd <dir>` | Print one JSON event per line (excluding your own replies) and exit on `submit`. Run it under a persistent Monitor. |
| `cc-review reply --comment <id> --kind <question\|option\|clarification> --body <text> [--option <text> ...]` | Post a reply under a comment. Returns immediately. |
| `cc-review reply --answer-to <replyId> --answer <text>` | Record your answer to a question (used during the post-submit drain). |
| `cc-review feedback --session <id> --cwd <dir>` | Print the frozen feedback JSON (`threads` + `open_questions`) after Submit. |
| `cc-review status [--session <id> --cwd <dir>]` | Show daemon and review status. |
| `cc-review stop` | Stop the background daemon. |

The daemon keeps state under `~/.cc-review` (sqlite db, per-review patch + feedback files, the control socket, and the HTTP handshake). It is never relocated by an environment variable.
