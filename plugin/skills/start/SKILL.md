---
name: start
description: Start or resume a cc-review review of the code Claude just wrote. Opens a PR-like web UI at a localhost URL, streams the human's inline comments back into this session in realtime so Claude can ask clarifying questions under each comment, and blocks edits until the human presses Submit. Use when the user asks to review changes, says "/review:start", "review this", "let me review before you change anything", or wants to give feedback on a diff before Claude proceeds.
---

# /review:start

You are running a code review. The human reviews your uncommitted changes in a browser; their comments stream to you here; you ask clarifying questions that render under each comment; you make **no edits** until they press **Submit**. Everything is CLI calls to `cc-review` — you are a thin wrapper around it.

`cc-review` is on `PATH`. If it's missing, run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"` once.

## 1. Start the review and give the user the URL

```bash
cc-review start --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

It prints two lines: a `http://127.0.0.1:<port>/s/<branch-slug>--<hash>` URL, then `channel: active` or `channel: inactive`. **Show the URL to the user verbatim** and tell them to open it and leave inline comments, then press **Submit** when done.

## 2. Wire up event delivery — then keep working

- **`channel: active`** — this session's MCP channel is streaming the review. Do **not** arm a Monitor (you would receive every event twice). Comments arrive as `<channel source="cc-review">` tags carrying the same JSON event payloads.
- **`channel: inactive`** — launch a **Monitor** (persistent) wrapping:

  ```bash
  cc-review watch --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
  ```

  Use the Monitor tool with `persistent: true` and a description like `cc-review comments`. Each line it prints is one JSON event; each becomes a chat notification.

Either way: **do not block waiting.** Tell the user you're watching and let their comments arrive. Events arrive on their own schedule; an event is not the user's reply.

## First run only: offer to silence the dev-channels warning

Once event delivery is wired up and you're idle, run this — **don't block the review on it**:

```bash
cc-review setup-channels --check
```

If it prints `{"offer":true,…}`, ask the user via **AskUserQuestion**: stop the *"Loading development channels"* confirmation that appears on every launch? cc-review gets added to Claude's approved channels (one macOS admin-password prompt).

- **Yes** — run `cc-review setup-channels --apply` (a password dialog appears). Then tell them: relaunch with `--channels plugin:review@cc-review` in place of `--dangerously-load-development-channels plugin:review@cc-review` — same channel, no warning.
- **No** — run `cc-review setup-channels --decline`.

Asked once either way. If `offer` is false or the command errors, skip silently. See `reference/channels-setup.md`.

## 3. React to each event — READ ONLY, make NO code changes

Each event (Monitor line or channel tag) is a JSON object with a `type`. The ones you act on:

- **`comment.created`** / **`comment.updated`** — the human left or updated a comment. The payload's `comment` has `filePath`, `range.start`, `lineContent`, and `body`. **`Read` the referenced file for context only.** Do not edit anything.
- **`submit`** — the human pressed Submit. Go to step 4.
- Other types (`comment.resolved`, `status.changed`, `notification`) are informational.

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

## 4. On the `submit` event — drain open questions, then proceed

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

## 5. Later rounds

After you make changes, the user can run `/review:start` again. It resumes the **same** review as a new version with a clean comment slate against the new diff — even from a new or rotated session — and all prior history is retained. `cc-review start --new` forces a fresh review instead.

## Reference

- `reference/cli-cheatsheet.md` — every `cc-review` command and flag.
- `reference/event-schema.md` — the event types and payload shapes.
- `reference/troubleshooting.md` — Monitor buffering, daemon, resume keying.
- `reference/channels.md` — opt-in: receive comments as `<channel>` tags instead of via Monitor.
- `reference/channels-setup.md` — the one-time offer that approves cc-review's channel so it loads without the dev-channels warning.
