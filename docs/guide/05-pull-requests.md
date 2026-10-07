---
title: Reviewing pull requests
description: Point cc-review at a GitHub pull request, review its whole stack, and keep every comment in sync with GitHub.
aliases:
  - /pull-requests/
---

cc-review reviews pull requests as well as your working tree. Point it at a PR and you get the PR's whole stack, one section per PR, in the same review UI. Your comments post to GitHub the moment you write them, your coworkers' comments flow back in, and Claude replies on GitHub under its own bot account, never as you.

## Set up GitHub once

PR mode talks to GitHub with two identities. Your own reads and writes use the token the GitHub CLI already holds (`gh auth token`), so sign in there first if you haven't:

```sh
gh auth login
```

Claude's replies use a GitHub App that belongs to you. Create it:

```sh
cc-review github setup
```

This opens a daemon page that hands GitHub an app manifest: an App named `cc-review-<your login>` with no webhook, permission to read contents, checks, and metadata, and permission to write pull requests. Confirm on GitHub and you're sent back to the daemon, which stores the App's id, slug, and bot login in `~/.cc-review/v1/github-app.json` and its private key in your Keychain. It then opens the App's install page: install it on the repos you review. Pass `--org <org>` to create the App under an organization instead of your account.

::: {.callout-note title="Organization repos"}
Installing an App you own on an organization's repos needs an organization owner's approval. GitHub files the request when you pick the org on the install page; until an owner approves it, Claude can't post on that org's PRs.
:::

Check where things stand at any point:

```sh
cc-review github status
```

It prints the App's slug and bot login, and whether the App is installed on the current directory's repo.

Why a separate App instead of your token? Coworkers reading a thread can tell your words from Claude's at a glance, since Claude's replies come from `cc-review-<your login>[bot]`. And cc-review needs to recognize Claude's comments when it reads the PR back, so it never hands Claude its own words as new feedback. There's no fallback to your token: when the App is missing or not installed, Claude's reply fails with the install URL in the error.

## Open a PR or a stack

Ask Claude to review the PR, by URL or number:

```
/cc-review:start https://github.com/acme/api/pull/123
```

The skill runs `start` with `--pr` and `--open`, so the review opens in your browser. After the `open-pr` skill ships a PR, it offers the same thing in one question: "Review #N in cc-review?" Without Claude, run the CLI from a checkout of the repo:

```sh
cc-review start --pr '#123' --open
```

`--pr` takes a URL, `owner/name#123`, `#123`, or `123`; the last two read the repo from the current directory's `origin`. `start` prints the usual lines plus one naming the stack:

```
pr: acme/api#123 (stack: #121 #122 #123 #124)
```

If the App needs attention, it also prints a second `setup:` line holding the command to run or the install URL to open.

The stack comes from GitHub, not from your local branches. cc-review walks down from the PR through each base branch's open PR until it reaches the default branch, then up through open PRs based on this PR's head. Going up, it stops at a fork: when two PRs build on the same branch, the review keeps the PR you named and everything below it. Each PR becomes one section, diffed against the merge base with its parent PR, trunk-most first. You don't need any of those branches checked out.

You can comment on any line GitHub accepts a comment on: a changed line, a context line inside a hunk, or the whole file. Lines you expand beyond a hunk are read-only in a PR section, because GitHub rejects a comment there.

## How sync works

### Your comments post immediately

A comment or reply you write in cc-review posts to GitHub right away, as you, through the GitHub CLI's token. It anchors to the head commit of the version you're reading. Resolving or reopening a thread does the same on GitHub. Nothing waits for Submit; Submit only sets your verdict.

### Sync states and retry

Every comment and reply you write carries a sync state. It reads `posting` while the GitHub call is in flight and `synced` once GitHub has it, with a link to the GitHub comment. A post GitHub rejects reads `failed`, shows GitHub's error, and offers a Retry button. A failure never disappears silently. The common cause is a PR head that moved after you loaded the version: GitHub refuses a comment anchored to a commit the PR no longer points at, and cc-review never re-anchors it behind your back.

### GitHub comments flow back in

A poller reads every PR in the stack with one GraphQL query: review threads, top-level comments, reviews, checks, and the head commit. It runs every 15 seconds while the review is open in a browser or Claude's channel is attached, backs off to every 2 minutes otherwise, and stops when the review closes. The GraphQL quota is shared with every other GitHub tool you run, so one query per stack per poll is a deliberate ceiling.

New GitHub comments land in the review as threads, matched by their GitHub id so nothing duplicates. Who wrote a comment decides how it's labeled and where it goes:

| GitHub author | Shows as | Reaches Claude |
| --- | --- | --- |
| You | your comment | yes, as your feedback |
| Your cc-review App (`cc-review-<login>[bot]`) | Claude's comment | no |
| Anyone else | the coworker, by name and avatar | yes, as coworker feedback |

Claude reads a coworker's comment like one of yours and can reply under it, and the reply posts to GitHub as the App. Claude never receives its own App's comments back, so a thread can't turn into Claude answering itself.

When a PR's head moves, cc-review captures a new version of the review, the same way a second `/cc-review:start` does for local work. A change to a PR's title, state, checks, or reviewers updates the PR header in place.

### Outdated threads

When a push moves the code out from under a thread, GitHub marks it outdated and drops its line number. cc-review keeps the thread and pins it to the file header, showing the line it was originally written on. File-level comments sit on the file header too.

### Submit and verdicts

Submit in a PR review asks for a verdict, Comment, Approve, or Request changes, plus an optional summary. cc-review posts one GitHub review per PR in the stack with that verdict and summary. It also freezes the feedback for Claude, exactly as in a local review.

GitHub doesn't let anyone approve or request changes on their own PR, so on PRs you authored those two verdicts are disabled, with the reason shown. That includes every PR Claude shipped from your account: on those, Comment is the verdict, and the threads carry the review.

After Submit, Claude works through the threads, coworkers' included, grouped by the PR each sits on. It fixes each PR on its own branch, trunk-most first, pushes, and rerunning `start --pr` picks the new heads up as the next version.

## The thin store

PR mode doesn't touch your working copy. It keeps one bare, blobless clone per repo at `~/.cc-review/v1/repos/<owner>/<name>.git`, cloned at depth 1 with no tags and a single branch. For each PR it fetches `refs/pull/<N>/head` and the PR's merge-base commit, again at depth 1. Git leaves file contents on GitHub until a diff needs them, so loading a stack fetches only the blobs of files the PRs change.

The cost is that the first view of a file needs the network. The store is derived state like everything else under `~/.cc-review/v1`: delete a repo's directory and the next PR review clones it again.
