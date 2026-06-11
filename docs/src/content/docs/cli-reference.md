---
title: CLI Reference
description: Every cc-review command, flag, and example invocation.
---

The plugin's SessionStart hook downloads the `cc-review` binary into `plugin/bin/cc-review` from the GitHub release matching the plugin version, and that directory is on `PATH` in plugin sessions. Every command is a thin shell around the daemon control client; the daemon lazy-starts on first use.

## start

```
cc-review start [--session <id>] [--cwd <dir>] [--new]
```

Start or resume a review of the working tree and print its URL. Also prints `channel: active` or `channel: inactive` depending on whether the MCP channel is connected.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--session` | string | `""` | Claude session id (keys the review with the repo root) |
| `--cwd` | string | `""` | working directory (defaults to the current directory) |
| `--new` | bool | `false` | force a fresh review, detaching any existing one for this session |

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

Make cc-review an approved Claude channel, silencing the dev-channels warning. Hidden from `--help`; `/review:start` runs it as a one-time offer. With no flag it behaves as `--check`.

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--check` | bool | `false` | print `{offer,reason}` JSON for the first-run offer (default) |
| `--apply` | bool | `false` | write the approved-channels config (prompts for admin) |
| `--decline` | bool | `false` | record that the offer was declined |

```sh
cc-review setup-channels --check
```

## Hidden internal commands

The binary also carries hidden entry points the plugin uses for itself. `daemon` runs the lazy-started background process, `session-record` handles the SessionStart hook, `guard-edit` handles the PreToolUse edit guard, and `mcp-channel` runs the MCP channel server. See [Internals](/cc-review/internals/) for how they fit together.
