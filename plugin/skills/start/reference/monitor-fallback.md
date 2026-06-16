# No Monitor tool: stream `watch` from a background streamer subagent

Some sessions expose no **Monitor** tool — Bedrock/Vertex/Foundry, telemetry disabled (`DISABLE_TELEMETRY`/`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`), headless/cron. Channels aren't active there either, so neither default route exists. Stream `watch` from a **background subagent** so none of the loop machinery lands in this thread.

A background task re-invokes its agent only when it **exits**, never per line — and `watch` blocks until `submit`. So make `watch` exit the instant it emits **one** event, deliver it, and relaunch. `watch` resumes from its cursor, so the relaunch gap loses nothing; while idle the task just blocks — no polling, no chatter — and wakes on the next comment.

## The exit-per-event command

The streamer runs this as a `run_in_background` Bash task and awaits its exit (`<SESSION>` = `$CLAUDE_CODE_SESSION_ID`, `<CWD>` = the repo root):

```sh
F="$(mktemp -u)"; mkfifo "$F"
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" watch --session "<SESSION>" --cwd "<CWD>" >"$F" 2>/dev/null &
W=$!
IFS= read -r ev <"$F"
kill "$W" 2>/dev/null; wait "$W" 2>/dev/null; rm -f "$F"
printf '%s\n' "$ev"
```

It blocks until one event, kills `watch` (so no orphaned second consumer races the cursor), prints that one JSON line, and exits. On `submit`, `watch` exits on its own and `ev` is the `submit` line. An empty `ev` means the daemon was down: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" status` (it lazy-starts), then relaunch.

## Spawn the streamer

Use the **Agent** tool, `run_in_background: true`, with this prompt — substitute the session id and repo root into the command:

> You are the cc-review event streamer. Run the exit-per-event `watch` command below as a `run_in_background` Bash task and await its exit; it prints exactly one JSON event line.
>
> ```sh
> <the command above, with <SESSION> and <CWD> filled in>
> ```
>
> - **If you have a `SendMessage` tool:** `SendMessage({to:"main"})` that raw line. If its `type` is `submit`, send it, then stop and complete. Otherwise relaunch and repeat.
> - **If you have no `SendMessage` tool:** return that one raw line as your final answer and complete — the main agent re-spawns you for the next event. If the line's `type` is `submit`, return it.
>
> If the command printed nothing, run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" status`, then retry. Never edit code, never reply to comments — you only relay raw event lines.

## React in the main agent

Each event reaches you as a streamer **message** (teams) or as the streamer's **completion result** (no teams). Read it from that message/result, not from the subagent's `.output` file — that file is the full transcript and floods your context. React exactly as step 4 of the skill: for `comment.created`/`comment.updated`, `Read` the file for context and optionally `reply`; for `ai.request.created`, dispatch the organize agent. Make no edits (the guard blocks them anyway).

When the event's `type` is `submit`, don't re-spawn the streamer — go to step 5 and drain open questions. Without teams, after reacting to a non-submit event, re-spawn the streamer (the same Agent call) for the next one. The streamer never sees your own `reply` events — `watch` filters `origin=claude` — so there's no echo.

## Floor: no subagent available

Only if you can't spawn a subagent at all, run the exit-per-event command inline as your own `run_in_background` Bash task and relaunch it per event until `submit`. This puts the loop in this thread — use it only when the **Agent** tool is unavailable.

## Submit, dedup, and edges

- **Submit** is a captured line whose JSON `type` is `submit`. `watch` has already exited; the streamer stops (resident) or completes and isn't re-spawned (one-shot). Then drain open questions.
- **Dedup** is unchanged: delivery is at-least-once, dedupe by event id. A cycle killed between the stdout write and the cursor sync may re-emit that one line — harmless (replies dedupe server-side, organize dedupes by request id).
- **Daemon restart mid-review:** `watch` reconnects internally (refreshing the handshake), so a single cycle survives it. A full outage shows as an empty `ev`, so run `status` and relaunch.
- **Plugin upgrade mid-review:** each cycle re-execs `${CLAUDE_PLUGIN_ROOT}/bin/cc-review`, so the next relaunch is already the new binary — nothing to re-arm.
- **Burst of comments:** each lands as its own fast cycle (cursor-resume), in order.
- **A `<channel>` tag arrives mid-fallback:** channels went live — run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, stop the streamer, and rely on tags. Same handover as the Monitor path.
- **`/resume`:** in-process subagents aren't restored — re-running `/cc-review:start` re-establishes the streamer.
- **Scope:** this serves interactive-but-Monitor-less and long-running scheduled sessions. A single-shot `claude -p` run reaps background tasks shortly after it returns and has no interactive reviewer, so the realtime model doesn't apply there.
