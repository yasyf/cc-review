# ![cc-review](docs/assets/readme-banner.webp)

**The only thing between Claude and its next edit is your Submit button.** cc-review renders your working tree as a local PR page and streams your comments to Claude live; a PreToolUse hook freezes edits until you press Submit.

[![CI](https://github.com/yasyf/cc-review/actions/workflows/ci.yml/badge.svg)](https://github.com/yasyf/cc-review/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yasyf/cc-review)](https://github.com/yasyf/cc-review/releases)
[![License: PolyForm Noncommercial](https://img.shields.io/badge/license-PolyForm--Noncommercial--1.0.0-blue)](LICENSE)

## Get started

```
/plugin marketplace add yasyf/cc-review
/plugin install cc-review@cc-review
```

Then, in a repo where Claude just wrote code, run `/cc-review:start` and open the printed URL:

<img src="docs/src/assets/screenshots/review-overview.png" alt="The cc-review web UI: a PR-style diff of the uncommitted working tree with a file tree, review progress, and an inline comment thread" width="700">

Driving with an agent? Paste this:

```
/plugin marketplace add yasyf/cc-review
/plugin install cc-review@cc-review
```

<details>
<summary>Standalone CLI, without the plugin (macOS)</summary>

```text
Install the cc-review CLI with `brew install yasyf/tap/cc-review`, then run
`cc-review start` in this repo and give me the review URL to open.
Docs: https://yasyf.github.io/cc-review
```

</details>

---

## Use cases

### Stop a wrong approach before Claude's next edit

Claude just rewrote your handler around a pattern you'd never merge, and it's about to keep going. Start a review:

```
/cc-review:start
```

Claude prints a `http://127.0.0.1:<port>/s/<slug>` URL and the edit guard engages — every `Edit` and `Write` is denied until you press Submit. Comment on the offending lines and the rework happens on your terms, not ten files later.

### Answer Claude's clarifying questions on the exact line they're about

In chat, "which config style do you want?" arrives forty lines of scrollback away from the code it's asking about. In the review UI, click the line and say what's wrong:

```text
This hardcodes the rate limit — should it come from config?
```

Claude replies under that comment in realtime — a clarifying question, a note, or an ask card with option buttons and a code preview. Pick an option and the answer lands in Claude's session immediately:

<img src="docs/src/assets/screenshots/comment-thread-ask.png" alt="A Claude ask card with selectable options rendered under an inline review comment" width="700">

### Resume yesterday's review against today's diff

You pressed Submit, Claude applied the feedback, and now you owe round two. Run the same command again:

```
/cc-review:start
```

The review resumes as a new version against the fresh diff — prior threads retained, files you already marked reviewed stay marked, and only files whose diff changed come back for re-reading. Everything persists in local SQLite under `~/.cc-review`, and a review idle for 24 hours expires on its own, so an abandoned one never wedges Claude.

## More in the docs

- **The edit guard** — what the PreToolUse hook blocks, when it lifts, and why it fails open — [how a review works](https://yasyf.github.io/cc-review/how-a-review-works/#the-edit-guard)
- **Asks and replies** — the three kinds of reply Claude posts under your comments — [Claude's side](https://yasyf.github.io/cc-review/how-a-review-works/#claudes-side)
- **Resume and versions** — how reviewed state carries across rounds — [resume semantics](https://yasyf.github.io/cc-review/how-a-review-works/#resume-and-versions)
- **CLI reference** — every `cc-review` subcommand and flag — [reference](https://yasyf.github.io/cc-review/cli-reference/)
- **Daemon internals** — lazy start, newest-wins version skew, unix-socket IPC, the SQLite schema — [internals](https://yasyf.github.io/cc-review/internals/)

Read the [docs](https://yasyf.github.io/cc-review) for the full guide. Licensed under [PolyForm Noncommercial 1.0.0](LICENSE).
