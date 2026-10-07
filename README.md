# ![cc-review](docs/assets/readme-banner.webp)

**The only thing between Claude and its next edit is your Submit button.** cc-review renders your working tree as a local PR page and streams your comments to Claude live; a PreToolUse hook freezes edits until you press Submit.

[![CI](https://github.com/yasyf/cc-review/actions/workflows/ci.yml/badge.svg)](https://github.com/yasyf/cc-review/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yasyf/cc-review)](https://github.com/yasyf/cc-review/releases)
[![PolyForm Noncommercial license](https://img.shields.io/badge/license-PolyForm--Noncommercial--1.0.0-blue)](LICENSE)

## Get started

```
/plugin marketplace add yasyf/cc-review
/plugin install cc-review@cc-review
```

Then, in a repo where Claude just wrote code, run `/cc-review:start` and open the printed URL:

<img src="docs/guide/images/review-overview.png" alt="The cc-review web UI: a PR-style diff of the uncommitted working tree with a file tree, review progress, and an inline comment thread" width="700">

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

Claude prints an `http://127.0.0.1:<port>/s/<slug>` URL and the edit guard engages, denying every `Edit` and `Write` until you press Submit. Comment on the offending lines and the rework happens on your terms, not ten files later.

### Answer Claude's clarifying questions on the exact line they're about

In chat, Claude's "which config style do you want" arrives forty lines of scrollback away from the code it's asking about. In the review UI, click the line and say what's wrong:

```text
This hardcodes the rate limit — should it come from config?
```

Claude replies under that comment in realtime, posting a clarifying question, a note, or an ask card with option buttons and a code preview. Pick an option and the answer lands in Claude's session immediately:

<img src="docs/guide/images/comment-thread-ask.png" alt="A Claude ask card with selectable options rendered under an inline review comment" width="700">

### Resume yesterday's review against today's diff

You pressed Submit, Claude applied the feedback, and now you owe round two. Run the same command again:

```
/cc-review:start
```

The review resumes as a new version against the fresh diff. Prior threads carry over, files you already marked reviewed stay marked, and only files whose diff changed come back for re-reading. Everything persists in the derived v1 namespace under `~/.cc-review/v1`, and a review idle for 24 hours expires on its own, so an abandoned one never wedges Claude.

### Review a teammate's PR stack without leaving the review UI

A coworker opened four stacked PRs and wants eyes on all of them. Point cc-review at any one:

```sh
cc-review start --pr '#123' --open
```

The whole stack loads as one review, a section per PR, pulled from GitHub through a blobless clone that never touches your checkout. Every comment you leave posts to GitHub as you write it, comments from everyone else stream back in, and Submit posts your verdict as one GitHub review per PR. Claude's replies post under its own `cc-review-<you>[bot]` account, so nobody mistakes them for yours.

## More in the docs

- [The edit guard](https://yasyf.github.io/cc-review/docs/guide/how-a-review-works.html#the-edit-guard) covers what the PreToolUse hook blocks, when it lifts, and why it fails open.
- [Claude's side](https://yasyf.github.io/cc-review/docs/guide/how-a-review-works.html#claudes-side) explains the three kinds of reply Claude posts under your comments.
- [Resume and versions](https://yasyf.github.io/cc-review/docs/guide/how-a-review-works.html#resume-and-versions) traces how reviewed state carries across rounds.
- [Reviewing pull requests](https://yasyf.github.io/cc-review/docs/guide/pull-requests.html) covers the one-time GitHub App setup, how stacks resolve, and how comments sync both ways.
- [CLI reference](https://yasyf.github.io/cc-review/docs/guide/cli-reference.html) lists every `cc-review` subcommand and flag.

Read the [docs](https://yasyf.github.io/cc-review) for the full guide. Licensed under [PolyForm Noncommercial 1.0.0](LICENSE).
