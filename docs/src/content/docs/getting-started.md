---
title: Getting Started
description: Install the cc-review plugin and run your first review of Claude's uncommitted changes.
---

cc-review puts a PR-style review step between Claude writing code and Claude committing to it. You review the uncommitted working tree in a local web UI, Claude responds under your comments in realtime, and a hook blocks further edits until you press Submit. This page gets you from nothing to a completed first review.

## Requirements

You need Claude Code and a git or jj repository. The prebuilt binary covers macOS and Linux on amd64 and arm64.

## Install

Inside Claude Code, add the marketplace and install the plugin:

```
/plugin marketplace add yasyf/cc-review
/plugin install review@cc-review
```

The plugin is self-contained. On your next session start, a SessionStart hook downloads the prebuilt `cc-review` binary from the GitHub release matching the plugin version. There is no Go toolchain or build step on your machine. When the plugin updates, the hook replaces the binary so the two stay in lockstep.

## Your first review

Have Claude make some changes. Ask it to fix a bug or add a small feature, and stop before it commits.

Start the review:

```
/review:start
```

Claude prints a URL of the form `http://127.0.0.1:<port>/s/<slug>` and tells you it is watching for comments. Open the URL in your browser. You get a familiar PR layout with a file tree on the left, syntax-highlighted diffs of the uncommitted working tree, and a header with the version, file count, review progress, and a Submit button.

Now click a line in the diff and leave an inline comment, the same kind you would write on any PR. It streams to Claude immediately.

Watch the thread. Claude reads the surrounding code and replies under your comment in the review UI, not in the chat window. A reply can be a clarifying question, a note, or an "ask", which renders as a structured card with option buttons, an optional code preview, and a notes field. Pick an option (or write your own) and submit the card; your answer goes straight back to Claude.

![A comment thread on ratelimit.go: a human comment about a hardcoded limit, with Claude's ask card offering Env var, Per-route option, and Other](../../assets/screenshots/comment-thread-ask.png)

While the review is open, Claude cannot edit files. A PreToolUse hook denies every edit until you submit, so nothing moves under you mid-review.

When you have said everything you want to say, press **Submit**. This freezes the feedback. Claude reads the full set of threads, asks you about any questions you left unanswered in the UI, and then applies the feedback to the code.

After Claude makes the changes, run `/review:start` again. It resumes the same review as a new version against the new diff, with all prior history retained.

## Nothing to review?

The diff is your uncommitted work, not your branch. In a git repo, `start` snapshots the working tree against `HEAD`, covering tracked, staged, and untracked files but skipping ignored ones. In a jj repo, it snapshots the working-copy change (`@`) against its parent. If Claude already committed the work, the diff is empty. Review before committing, or reset the work back into the working tree.

## Next

Read [How a review works](/cc-review/how-a-review-works/) for the full lifecycle, from events and replies to the edit guard and resume semantics.
