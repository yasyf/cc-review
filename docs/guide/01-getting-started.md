---
title: Your first review in five minutes
description: Install the cc-review plugin and run your first review of Claude's uncommitted changes.
aliases:
  - /getting-started/
---

cc-review puts a PR-style review step between Claude writing code and Claude committing to it. You review the uncommitted working tree in a local web UI, Claude responds under your comments in realtime, and a hook blocks further edits until you press Submit.

Here's where you land: a submitted review, in which you commented on Claude's diff from your browser, answered its ask card inline, and pressed Submit — with Claude unable to touch a file until you did. Claude then applies exactly the feedback you froze.

## Requirements

You need Claude Code and a git or jj repository. The prebuilt binary covers macOS and Linux on amd64 and arm64.

## Install

Inside Claude Code, add the marketplace and install the plugin:

```
/plugin marketplace add yasyf/cc-review
/plugin install cc-review@cc-review
```

The plugin is self-contained. On your next session start, a SessionStart hook downloads the prebuilt `cc-review` binary from the GitHub release matching the plugin version. There is no Go toolchain or build step on your machine. When the plugin updates, the hook replaces the binary so the two stay in lockstep.

To install the `cc-review` CLI on its own (macOS), use Homebrew:

```
brew install yasyf/tap/cc-review
```

## Your first review

1. Have Claude make some changes. Ask it to fix a bug or add a small feature, and stop before it commits.

2. Start the review:

   ```
   /cc-review:start
   ```

::: {.callout-tip title="Checkpoint"}
Claude prints a review URL of the form `http://127.0.0.1:<port>/s/<slug>` and tells you it is watching for comments. An empty diff means the work is already committed — reset it back into the working tree and start again (see [Nothing to review?](#nothing-to-review)).
:::

3. Open the URL in your browser. You get a familiar PR layout with a file tree on the left, syntax-highlighted diffs of the uncommitted working tree, and a header with the version, file count, review progress, and a Submit button.

4. Click a line in the diff and leave an inline comment, the same kind you would write on any PR. It streams to Claude immediately.

5. Watch the thread. Claude reads the surrounding code and replies under your comment in the review UI, not in the chat window. A reply can be a clarifying question, a note, or an "ask", which renders as a structured card with option buttons, an optional code preview, and a notes field. Pick an option (or write your own) and submit the card; your answer goes straight back to Claude.

   ![A comment thread on ratelimit.go: a human comment about a hardcoded limit, with Claude's ask card offering Env var, Per-route option, and Other](images/comment-thread-ask.png)

::: {.callout-tip title="Checkpoint"}
Claude's reply renders under your comment in the browser, and while the review is open every edit is denied — a PreToolUse hook holds `Edit`, `Write`, and `NotebookEdit` until you submit. `cc-review list` shows the review as an `open` row.
:::

6. When you have said everything you want to say, press **Submit**. This freezes the feedback. Claude reads the full set of threads, asks you about any questions you left unanswered in the UI, and then applies the feedback to the code.

7. After Claude makes the changes, run `/cc-review:start` again. It resumes the same review as a new version against the new diff, with all prior history retained.

A review idle for 24 hours expires on its own and unblocks edits; `cc-review close` ends one without submitting, and `cc-review list` shows every open review.

## Nothing to review?

The diff is your uncommitted work, not your branch. In a git repo, `start` snapshots the working tree against `HEAD`, covering tracked, staged, and untracked files but skipping ignored ones. In a jj repo, it snapshots the working-copy change (`@`) against its parent. If Claude already committed the work, the diff is empty. Review before committing, or reset the work back into the working tree.

## Next

Read [How a review works](how-a-review-works.md) for the full lifecycle, from events and replies to the edit guard and resume semantics.
