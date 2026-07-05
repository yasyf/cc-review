---
title: CLI Reference
description: Every cc-review command, flag, and example invocation.
---

The plugin's SessionStart hook provisions the `cc-review` binary pinned by the plugin version — a brew install wins, otherwise a checksum-verified download to the plugin data dir — and points the `plugin/bin/cc-review` symlink at it; that directory is on `PATH` in plugin sessions. Every command is a thin shell around the daemon control client; the daemon lazy-starts on first use.

## start

```
cc-review start [--session <id>] [--cwd <dir>] [--new] [--base <ref>]
```

Start or resume a review of the working tree. Prints, in order: the review URL; `channel: active|pending|inactive` — `active` when this window's channel is proven (its first delivered tag was acknowledged via `channel-ack`) and the channel consumer is attached, `pending` when the channel server is attached but the window is unproven — the normal state before any tag has been acked; that start also injects a one-shot `channel.probe` tag to solicit the proof — `inactive` when no channel consumer exists; `setup: {"offer":…,"reason":…}` — the first-run channel-approval offer (always printed; an offer-check error degrades to `offer: false` with the error as the reason); and `organize: <AI request JSON>` — the daemon's eager organize request, printed when a new version was created and re-offered (same id) when an unchanged resume finds the latest version still unorganized.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id (keys the review with the repo root) |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |
| `--new` | bool | `false` | force a fresh review, detaching any existing one for this session |
| `--base` | string | `""` | pin a new review's diff base: the fork point of this ref and the working copy (default: HEAD, falling back to trunk when the working tree is clean) |

```sh
cc-review start --session "$CLAUDE_SESSION_ID"
```

## watch

```
cc-review watch [--session <id>] [--cwd <dir>]
```

Stream review events as line-delimited JSON, one event per line, then exit on the terminal `submit` event. Output is line-buffered and resumes from a persisted cursor, so a restart re-delivers nothing it already emitted. Meant to run under a Claude Code Monitor so each line becomes a chat notification.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |

```sh
cc-review watch --session "$CLAUDE_SESSION_ID"
```

## reply

```
cc-review reply --comment <id> [--kind <kind>] [--body <text>] ...
cc-review reply --answer-to <id> (--answer <text> | --select <label> [--other <text>] [--notes <text>])
```

Post a Claude question, ask, or clarification under a comment, or answer one. Returns immediately. The two modes are exclusive. `--comment` posts a new reply; `--answer-to` records an answer to an existing one. Flags from the other mode are rejected, not ignored.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--comment` | int64 | `0` | comment id to reply under |
| `--kind` | string | `clarification` | reply kind: `question` \| `ask` \| `clarification` |
| `--body` | string | `""` | reply text (the question for `--kind ask`) |
| `--header` | string | `""` | short chip shown on the ask card, e.g. Approach |
| `--multi-select` | bool | `false` | allow picking multiple options |
| `--options-json` | string | `""` | JSON array of `{"label","description"?,"preview"?}` (requires `--kind ask`) |
| `--answer-to` | int64 | `0` | the reply id of the question or ask being answered |
| `--answer` | string | `""` | the answer text when answering a plain question |
| `--select` | string array | `[]` | a chosen option label when answering an ask (repeatable) |
| `--other` | string | `""` | free-text answer outside the offered options |
| `--notes` | string | `""` | a note riding along with the selection |

Post an ask card under comment 12:

```sh
cc-review reply --comment 12 --kind ask \
  --body "How should the limit be configured?" --header "Config" \
  --options-json '[{"label":"Env var"},{"label":"Per-route option"}]'
```

Answer that ask:

```sh
cc-review reply --answer-to 7 --select "Env var" --notes "Default to 100"
```

## feedback

```
cc-review feedback [--session <id>] [--cwd <dir>]
```

Print the frozen feedback JSON after Submit. The payload contains the comment threads and any open questions.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |

```sh
cc-review feedback --session "$CLAUDE_SESSION_ID"
```

## status

```
cc-review status [--session <id>] [--cwd <dir>]
```

Show daemon and review status. Prints the daemon version, HTTP port, and the review id and state for this session/repo. Reports `daemon: not running` without spawning one.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |

```sh
cc-review status
```

## list

```
cc-review list [--cwd <dir>]
```

List every open or expired review across repos, one `SLUG STATUS AGE IDLE SCOPE` row each: status, age since creation, idle time since the last real activity, and the repo. Real activity is comments, replies, AI-bar requests, and new versions; presence pings don't count. Prints `no open reviews` when there are none. The listing spans all repos and runs from anywhere.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |

```sh
cc-review list
```

## close

```
cc-review close [review] [--session <id>] [--cwd <dir>] [--stale]
```

Close a review without submitting. With no argument, closes the current window's review; pass a slug or id to close any review. `--stale` instead closes every expired review across all repos — sweeping open reviews idle past the 24-hour TTL into `expired` first — and rejects an explicit review argument. A closed review never resumes; an expired one stays bound to its window, so an explicit `start` reopens it. Prints one line per review closed. `--stale` spans all repos and runs from anywhere.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id (keys the review with the repo root) |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |
| `--stale` | bool | `false` | close every expired review across all repos, sweeping idle ones past the TTL first |

```sh
cc-review close
```

## stop

```
cc-review stop
```

Stop the background daemon. No flags.

```sh
cc-review stop
```

## setup-channels

```
cc-review setup-channels [--check | --apply | --decline]
```

Make cc-review an approved Claude channel, silencing the dev-channels warning. Hidden from `--help`. `cc-review start` already prints the offer check as its `setup:` line; `/cc-review:start` reads that line and runs `--apply` or `--decline` once based on the user's answer. With no flag it behaves as `--check`.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--check` | bool | `false` | print `{offer,reason}` JSON for the first-run offer (default; the same JSON `start` prints as `setup:`) |
| `--apply` | bool | `false` | write the approved-channels config (prompts for admin) |
| `--decline` | bool | `false` | record that the offer was declined |

```sh
cc-review setup-channels --check
```

## Hidden internal commands

The binary also carries hidden entry points the plugin uses for itself. `daemon` runs the lazy-started background process, `session-record` handles the SessionStart hook, `guard-edit` handles the PreToolUse edit guard, `mcp-channel` runs the MCP channel server, and `channel-ack` marks a window's channel proven after its first delivered tag. See [Internals](/cc-review/internals/) for how they fit together.
