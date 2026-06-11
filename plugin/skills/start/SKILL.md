---
name: start
description: Start or resume a cc-review review of the code Claude just wrote. Opens a PR-like web UI at a localhost URL, streams the human's inline comments back into this session in realtime so Claude can ask clarifying questions under each comment, and blocks edits until the human presses Submit. Use when the user asks to review changes, says "/cc-review:start", "review this", "let me review before you change anything", or wants to give feedback on a diff before Claude proceeds.
---

# /cc-review:start

You are running a code review. The human reviews your uncommitted changes in a browser; their comments stream to you here; you ask clarifying questions that render under each comment; you make **no edits** until they press **Submit**. Everything is CLI calls to `cc-review` — you are a thin wrapper around it.

`cc-review` is on `PATH`. If it's missing, run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"` once.

## 1. Start the review and give the user the URL

```bash
cc-review start --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

It prints, in order:

```
http://127.0.0.1:<port>/s/<branch-slug>--<hash>
channel: active|pending|inactive
setup: {"offer":<bool>,"reason":"<string>"}
organize: {"id":"<n>","source":"system","prompt":"Organize this review into chapters and rate per-file risk.","status":"pending","summary":"","unmatched":[],"changes":[],"createdAt":"<RFC3339 UTC>","updatedAt":"<RFC3339 UTC>"}
```

The `organize:` line appears when a new version needs organizing, and again when an unchanged resume finds the latest version still unorganized — the daemon re-offers the still-open request with the same `id`. A new version identical to the last organized one, whose organization the daemon carries forward itself, omits it. **Show the URL to the user verbatim** and tell them to open it and leave inline comments, then press **Submit** when done.

## 2. When `organize:` is present, dispatch the organize agent

Use the **Agent** tool with `subagent_type: "cc-review:organize"`, `run_in_background: true`, and the `organize:` line's JSON, verbatim, as the prompt. Don't wait for it — show the user the URL and move on. The agent builds the chapters and closes the request itself. Remember the request `id`: the daemon redelivers the same request as an `ai.request.created` event, which you ignore (step 4). If the `organize:` line's `id` is one you already dispatched in this conversation — the daemon re-offers a still-open request on resume — do not dispatch again.

## 3. Wire up event delivery — then keep working

- **`channel: active`** — this window's channel is proven and streaming the review. Do **not** arm a Monitor (you would receive every event twice). Comments arrive as `<channel source="cc-review">` tags carrying the same JSON event payloads.
- **`channel: pending`** or **`channel: inactive`** — launch a **Monitor** (persistent) wrapping:

  ```bash
  cc-review watch --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
  ```

  Use the Monitor tool with `persistent: true` and a description like `cc-review comments`. Each line it prints is one JSON event; each becomes a chat notification. `pending` means the channel server is wired but unproven — Claude Code may be silently dropping its notifications — so the Monitor is the route.

If a `<channel source="cc-review">` tag arrives while the Monitor is armed, channels are live: run `cc-review channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, stop the Monitor with **TaskStop**, and rely on tags from then on — dedupe the brief overlap by event id. Delivery is at-least-once: a Monitor re-armed in a later session may replay events you already handled as tags; treat already-handled events as informational (replies dedupe server-side, organize dispatch dedupes by request id).

Either way: **do not block waiting.** Tell the user you're watching and let their comments arrive. Events arrive on their own schedule; an event is not the user's reply.

## First run only: offer to silence the dev-channels warning

The `setup:` line from step 1 is the offer check. If it printed `"offer":true`, once event delivery is wired up and you're idle — **don't block the review on it** — ask the user via **AskUserQuestion**: stop the *"Loading development channels"* confirmation that appears on every launch? cc-review gets added to Claude's approved channels (one macOS admin-password prompt).

- **Yes** — run `cc-review setup-channels --apply` (a password dialog appears). Then tell them: relaunch with `--channels plugin:cc-review@cc-review` in place of `--dangerously-load-development-channels plugin:cc-review@cc-review` — same channel, no warning.
- **No** — run `cc-review setup-channels --decline`.

Asked once either way. If `offer` is false, skip silently — `reason` says why. See `reference/channels-setup.md`.

## 4. React to each event — READ ONLY, make NO code changes

Each event (Monitor line or channel tag) is a JSON object with a `type`. The ones you act on:

- **`comment.created`** / **`comment.updated`** — the human left or updated a comment. The payload's `comment` has `filePath`, `range.start`, `lineContent`, and `body`. **`Read` the referenced file for context only.** Do not edit anything. When a reply or answer materially changes a file's risk read, dispatch the organize agent in the background — the same **Agent** tool invocation as step 2, `run_in_background: true` — with a one-line re-rank prompt: the new fact plus the file path. Replies themselves stay in the main session.
- **`ai.request.created`** — the daemon (auto-organize) or the human's AI bar is asking for review work. Dispatch exactly as in step 2 — the **Agent** tool, `subagent_type: "cc-review:organize"`, `run_in_background: true`, the event's `request` JSON as the prompt — **unless** `request.id` equals the id you already dispatched from start output (the same request redelivered): ignore it. Dedupe by exact id only.
- **`submit`** — the human pressed Submit. Go to step 5.
- Other types (`comment.resolved`, `status.changed`, `notification`, `file.states`, `ai.request.updated`, `version.created`, `channel.changed`) are informational — `file.states` and `ai.request.updated` carry the human's checkboxes, an undo, the daemon unmarking changed files, or the daemon closing an organize request it carried forward (`status: "done"`); events from your own tool calls are filtered out and never echo back. `organization.updated` never reaches you — it originates from the organize agent's `submit_organization` (or the daemon carrying the organization forward onto an identical new version) and only the browser renders it.

If a comment is ambiguous or you see options worth surfacing, post back — it renders under that comment in realtime:

```bash
# a clarifying question
cc-review reply --comment <commentId> --kind question --body "Did you mean X or Y here?"
# a structured ask (renders as an AskUserQuestion-style card)
cc-review reply --comment <commentId> --kind ask --body "Which approach?" \
  --header "Approach" [--multi-select] \
  --options-json '[{"label":"Keep as-is","description":"why..."},{"label":"Extract a helper","description":"why...","preview":"code or markdown shown in a side pane"}]'
# a free-form note
cc-review reply --comment <commentId> --kind clarification --body "Note: this also affects callers in foo.go"
```

`reply` returns immediately. Then go back to waiting for the next notification. **Never edit code in this phase.** A hook blocks edits until Submit anyway.

## 5. On the `submit` event — drain open questions, then proceed

The submit signal is the Monitor's final line (it exits) on the Monitor path, or a channel tag whose JSON `type` is `submit` on the channel path. Now:

```bash
cc-review feedback --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

This prints the frozen feedback JSON: `threads` (every comment + the back-and-forth) and `open_questions` (your questions the human didn't answer in the UI). Asks the human already answered in the web UI arrived earlier as `comment.updated` events — the ask reply carries `answered: true` and `askAnswer` — and are not in `open_questions`; don't re-ask them. For each open question, ask the human via **AskUserQuestion** (≤4 per call; loop if there are more). When the entry carries `ask`, map it 1:1 onto AskUserQuestion — the field names match: `question` is the question text, `ask.header` the header, `ask.options[].label`/`description` the options, `ask.multiSelect` the multiSelect (absent means false), and an option's `preview` the option preview. Write the pick back:

```bash
cc-review reply --answer-to <replyId> --select "<label>" [--select "<label>"] [--other "<free text>"] [--notes "<note>"]
```

For a plain `question` entry (no `ask`), write back free text instead:

```bash
cc-review reply --answer-to <replyId> --answer "<the human's answer>"
```

**Only after the open questions are drained do you make code changes.** Apply the feedback from `threads` (and the answers) to the code.

## 6. Later rounds

After you make changes, the user can run `/cc-review:start` again. It resumes the **same** review as a new version with a clean comment slate against the new diff — across `/clear` and resume in the same Claude window — and all prior history is retained. `cc-review start --new` forces a fresh review instead. The daemon carries reviewed state forward and unmarks only the files whose diff changed — never touch reviewed flags because the version changed. It also carries the chapter organization forward when the new diff is identical to the last organized one.

## Reference

- `reference/cli-cheatsheet.md` — every `cc-review` command and flag.
- `reference/event-schema.md` — the event types and payload shapes.
- `reference/troubleshooting.md` — Monitor buffering, daemon, resume keying.
- `reference/channels.md` — opt-in: receive review events as `<channel>` tags instead of via Monitor.
- `reference/channels-setup.md` — the one-time offer that approves cc-review's channel so it loads without the dev-channels warning.
