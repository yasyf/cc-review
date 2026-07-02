---
name: start
description: Start or resume a cc-review review of the code Claude just wrote. Opens a PR-like web UI at a localhost URL, streams the human's inline comments back into this session in realtime so Claude can ask clarifying questions under each comment, and blocks edits until the human presses Submit (a review idle for 24 hours expires and lifts the block). Use when the user asks to review changes, says "/cc-review:start", "review this", "let me review before you change anything", or wants to give feedback on a diff before Claude proceeds.
---

# /cc-review:start

You are running a code review. The human reviews your uncommitted changes in a browser; their comments stream to you here; you ask clarifying questions that render under each comment; you make **no edits** until they press **Submit**. Everything is CLI calls to `cc-review` — you are a thin wrapper around it.

The binary is `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review"` — always invoke it by that absolute path, never as bare `cc-review`. If it's missing, run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"` once.

## 1. Start the review and give the user the URL

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" start --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

It prints, in order:

```
http://127.0.0.1:<port>/s/<branch-slug>--<hash>
channel: active|pending|inactive
setup: {"offer":<bool>,"reason":"<string>"}
organize: {"id":"<n>","source":"system","prompt":"Organize this review into chapters and rate per-file risk.","status":"pending","summary":"","unmatched":[],"changes":[],"createdAt":"<RFC3339 UTC>","updatedAt":"<RFC3339 UTC>"}
```

There can be **zero or more** `organize:` lines — one per request still open on the current version. The first is usually the system organize (`source: "system"`), present when a new version needs organizing or an unchanged resume finds the latest version still unorganized. The rest, `source: "user"`, are the human's AI-bar prompts that were submitted while no live session was attached and so never ran — the daemon re-offers each here so this freshly attached session dispatches it. A new version identical to the last organized one, whose organization the daemon carries forward itself, omits the system organize but still re-offers any open user request. **Show the URL to the user verbatim** and tell them to open it and leave inline comments, then press **Submit** when done.

## 2. For each `organize:` line, dispatch the organize agent

For **every** `organize:` line, dispatch a `subagent_type: "cc-review:organize"` agent with that line's JSON, verbatim, as the prompt, on the **full current model** (no `model` override). **How** to dispatch depends on your route (step 3): on every route **except teams**, use the **Agent** tool with `run_in_background: true`; on the **teams** route spawn it as a `cc-review:organize` **teammate** instead — an in-process team forbids the lead from spawning background agents, so organize runs as a teammate that closes its own request. `source: "user"` lines carry a human's AI-bar request and the agent already handles them. Don't wait for any of them — show the user the URL and move on. The agent builds the chapters (or executes the request) and closes it itself. Remember each request's `(id, attempt)`: the daemon redelivers the same request as an `ai.request.created` event, which you ignore (step 4). **Dedupe dispatch by `(id, attempt)`**: an `organize:` line whose `id` *and* `attempt` you already dispatched this conversation is a resume re-offer — skip it. A line whose `id` you've seen but with a **higher** `attempt` (and `status: "answered"`, carrying `question` + `answer`) is a request the reviewer answered after the agent asked a clarifying question — **dispatch it**, so the agent resumes with that answer.

## 3. Wire up event delivery — pick exactly one route

Events reach you through **one** route, chosen once by the first matching condition. Open that route's reference file and follow it — each is a complete, self-contained workflow. Do not blend routes.

| # | If… | Route | Follow |
|---|------|-------|--------|
| 1 | `channel: active` (from step 1) | channel tags | `reference/route-channel.md` |
| 2 | else you have a **Monitor** tool | Monitor on `watch` | `reference/route-monitor.md` |
| 3 | else you have a **SendMessage** tool (agent teams are on) | teams: streamer teammate | `reference/route-teams.md` |
| 4 | else you have an **Agent** tool | one-shot streamer subagent | `reference/route-streamer.md` |
| 5 | else | inline `watch --once` in this thread | `reference/route-inline.md` |

`channel: pending` or `inactive` (from step 1) means the channel isn't proven — fall through to row 2+. On `pending`, start has already injected a `channel.probe` tag to prove delivery, so expect the transition below within seconds when channels work. Every **non-channel** route shares one transition: if a `<channel source="cc-review">` tag arrives while it runs, channels went live — run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, tear the route down, and switch to `route-channel`. Delivery is at-least-once; dedupe any overlap by event id (replies dedupe server-side, organize dispatch dedupes by request `(id, attempt)`).

**Do not block waiting.** Whichever route you pick, tell the user you're watching and keep working — events arrive on their own schedule, and an event is not the user's reply.

## First run only: offer to approve the channel

The `setup:` line from step 1 is the offer check. If it printed `"offer":true`, once event delivery is wired up and you're idle — **don't block the review on it** — ask the user via **AskUserQuestion**: approve cc-review as a Claude channel? It goes on the approved allowlist in managed settings (one macOS admin-password prompt), so `--channels` launches load it with no dev-channels warning.

- **Yes** — run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" setup-channels --apply` (a password dialog appears). Then tell them: launch with `--channels plugin:cc-review@cc-review` (replacing `--dangerously-load-development-channels plugin:cc-review@cc-review` if they used it) — same channel, no warning.
- **No** — run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" setup-channels --decline`.

Asked once either way. If `offer` is false, skip silently — `reason` says why. See `reference/channels-setup.md`.

## 4. React to each event — READ ONLY, make NO code changes

Each event (a Monitor line, a channel tag, or a streamer message/result) is a JSON object with a `type`. The ones you act on:

- **`comment.created`** / **`comment.updated`** — the human left or updated a comment. The payload's `comment` has `filePath`, `range.start`, `lineContent`, and `body`. **`Read` the referenced file for context only.** Do not edit anything. When a reply or answer materially changes a file's risk read, dispatch a re-rank organize agent the same way as step 2 (a background agent, or a `cc-review:organize` teammate on the teams route) with a one-line re-rank prompt: the new fact plus the file path. Replies themselves stay in the main session.
- **`ai.request.created`** — the daemon (auto-organize), the human's AI bar, or the human answering a clarifying question is asking for review work. Dispatch exactly as in step 2 (a background `cc-review:organize` agent, or a `cc-review:organize` teammate on the teams route), the event's `request` JSON as the prompt — **unless** its `(id, attempt)` matches one you already dispatched (the same request redelivered): ignore it. Dedupe by `(id, attempt)`; a **higher** `attempt` for a known id is the reviewer's answer to the agent's clarifying question (`status: "answered"`) — dispatch it.
- **`submit`** — the human pressed Submit. Go to step 5.
- Other types (`comment.resolved`, `status.changed`, `notification`, `file.states`, `ai.request.updated`, `version.created`) are informational — `file.states` and `ai.request.updated` carry the human's checkboxes, an undo, the daemon unmarking changed files, or the daemon closing an organize request it carried forward (`status: "done"`); events from your own tool calls are filtered out and never echo back. `organization.updated` and `channel.changed` never reach you — `organization.updated` originates from the organize agent's `submit_organization` (or the daemon carrying the organization forward onto an identical new version) and only the browser renders it; `channel.changed` drives the browser's Claude-connected indicator and only the browser receives it.

If a comment is ambiguous or you see options worth surfacing, post back — it renders under that comment in realtime:

```bash
# a clarifying question
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" reply --comment <commentId> --kind question --body "Did you mean X or Y here?"
# a structured ask (renders as an AskUserQuestion-style card)
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" reply --comment <commentId> --kind ask --body "Which approach?" \
  --header "Approach" [--multi-select] \
  --options-json '[{"label":"Keep as-is","description":"why..."},{"label":"Extract a helper","description":"why...","preview":"code or markdown shown in a side pane"}]'
# a free-form note
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" reply --comment <commentId> --kind clarification --body "Note: this also affects callers in foo.go"
```

`reply` returns immediately. Then go back to waiting for the next notification. **Never edit code in this phase.** A hook blocks edits until Submit anyway; a review idle for 24 hours expires on its own and the hook lifts.

A `status.changed` event with `expired` or `closed` also ends the round: no `submit` will follow, so stop waiting and proceed without feedback (skip step 5 — there is nothing to drain).

## 5. On the `submit` event — drain open questions, then proceed

The submit signal is the Monitor's final line on `route-monitor`, a channel tag whose `type` is `submit` on `route-channel`, or a relayed line whose `type` is `submit` on the teams/streamer/inline routes — after which you stop the streamer teammate (teams) or simply stop re-spawning/relaunching the watcher. Now:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" feedback --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

This prints the frozen feedback JSON: `threads` (every comment + the back-and-forth) and `open_questions` (your questions the human didn't answer in the UI). Asks the human already answered in the web UI arrived earlier as `comment.updated` events — the ask reply carries `answered: true` and `askAnswer` — and are not in `open_questions`; don't re-ask them. For each open question, ask the human via **AskUserQuestion** (≤4 per call; loop if there are more). When the entry carries `ask`, map it 1:1 onto AskUserQuestion — the field names match: `question` is the question text, `ask.header` the header, `ask.options[].label`/`description` the options, `ask.multiSelect` the multiSelect (absent means false), and an option's `preview` the option preview. Write the pick back:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" reply --answer-to <replyId> --select "<label>" [--select "<label>"] [--other "<free text>"] [--notes "<note>"]
```

For a plain `question` entry (no `ask`), write back free text instead:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" reply --answer-to <replyId> --answer "<the human's answer>"
```

**Only after the open questions are drained do you make code changes.** Apply the feedback from `threads` (and the answers) to the code.

## 6. Later rounds

After you make changes, the user can run `/cc-review:start` again. It resumes the **same** review as a new version with a clean comment slate against the new diff — across `/clear` and resume in the same Claude window — and all prior history is retained. Passing `--new` to `start` forces a fresh review instead. The daemon carries reviewed state forward and unmarks only the files whose diff changed — never touch reviewed flags because the version changed. It also carries the chapter organization forward when the new diff is identical to the last organized one.

## Reference

Event-delivery routes — step 3 picks exactly one:
- `reference/route-channel.md` — events as `<channel>` tags (no Monitor).
- `reference/route-monitor.md` — a persistent Monitor wrapping `watch`.
- `reference/route-teams.md` — agent teams on: a streamer teammate relays; organize runs as a teammate.
- `reference/route-streamer.md` — no teams: a one-shot Haiku streamer subagent relays.
- `reference/route-inline.md` — floor: run `watch --once` in this thread.

- `reference/cli-cheatsheet.md` — every `cc-review` command and flag.
- `reference/event-schema.md` — the event types and payload shapes.
- `reference/troubleshooting.md` — Monitor buffering, daemon, resume keying.
- `reference/channels-setup.md` — the one-time offer that approves cc-review's channel so it loads without the dev-channels warning.
