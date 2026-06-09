# cc-review

[![CI](https://img.shields.io/github/actions/workflow/status/yasyf/cc-review/ci.yml?branch=main&label=CI)](https://github.com/yasyf/cc-review/actions/workflows/ci.yml)
[![License: PolyForm-Noncommercial-1.0.0](https://img.shields.io/badge/License-PolyForm--Noncommercial--1.0.0-blue.svg)](https://github.com/yasyf/cc-review/blob/main/LICENSE)

Local code-review daemon + Claude plugin — review what Claude writes in a PR-like web UI and stream feedback back.

cc-review lets you review the code Claude just wrote in a GitHub-PR-style web page
*before* it commits to changes: you leave inline comments, Claude answers your
questions and proposes options right under each comment in realtime, and only once
you press **Submit** does Claude proceed. Every review and the full back-and-forth is
kept forever in a local SQLite, and re-running `/review:start` resumes the same review
against your latest changes.

## Install

cc-review is a Claude Code plugin that ships a prebuilt `cc-review` binary (the CLI +
lazily-started local daemon). Add the marketplace and install the plugin:

```
/plugin marketplace add yasyf/cc-review
/plugin install review@cc-review
```

## Quickstart

In any git repo with uncommitted changes Claude just made, run:

```
/review:start
```

Claude prints a `http://127.0.0.1:<port>/s/<id>` URL. Open it to see a PR-style diff of
the working tree, leave inline comments, and watch Claude reply with questions and
options under each one. Press **Submit** at the top when you're done — Claude reads the
frozen feedback, asks any leftover questions inline, and then makes the changes. Run
`/review:start` again after those changes to resume the review against the new diff.

## What problems does this solve?

- **Review-before-change.** You see and shape what Claude is about to do in a familiar
  PR UI instead of discovering it after the edits land — a `PreToolUse` hook hard-blocks
  edits until you submit.
- **A real back-channel.** Claude's clarifying questions and option sets render *under*
  your comments in realtime, so the conversation stays anchored to specific lines.
- **Nothing is lost.** Every comment, reply, and decision is persisted forever in local
  SQLite; reviews resume across sessions, keyed to your Claude session.
- **Fully local, lazily started.** One Go binary serves the UI and daemon on
  `127.0.0.1`; no cloud, no account, no server to keep running.

## License

PolyForm-Noncommercial-1.0.0. See [LICENSE](https://github.com/yasyf/cc-review/blob/main/LICENSE).
