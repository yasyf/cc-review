# Route: one-shot streamer subagent (no teams)

You have an **Agent** tool but no Monitor, no active channel, and no SendMessage (agent teams are off). Relay `watch` through a **one-shot streamer subagent** that runs in the background and reports each event via its completion result. No team forms, so you stay the orchestrator and can dispatch background organize agents.

## Spawn the streamer

Use the **Agent** tool, `run_in_background: true`, `model: "haiku"` (it only runs a command and relays one line), with this prompt — substitute the session id and repo root:

> You are the cc-review event streamer. Run the command below as a `run_in_background` Bash task and await its exit — it prints exactly one JSON event line. Return that one raw line as your final answer and complete. Never run it in the foreground — a quiet stretch would hit the 10-minute Bash timeout and end your turn. Never edit code or reply to comments; you only relay one raw line.
>
> ```sh
> "${CLAUDE_PLUGIN_ROOT}/bin/cc-review" watch --once --session "<SESSION>" --cwd "<CWD>"
> ```

`watch --once` exits the instant one event arrives and resumes from its cursor on relaunch, so re-spawning loses nothing.

## React, then re-spawn

The streamer's completion result is one event line — read it from the result, not the subagent's `.output` file (that floods your context). React per skill step 4. You are **not** in a team, so dispatch organize as a background **Agent** (`subagent_type: "cc-review:organize"`, `run_in_background: true`, full model). Then **re-spawn the streamer** (the same Agent call) for the next event. On `submit`, don't re-spawn — drain open questions (step 5).

## Channel upgrade

If a `<channel source="cc-review">` tag arrives, channels went live: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, stop re-spawning the streamer, and switch to `route-channel`.

## Edges

- An empty result means the daemon was down: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" status` (it lazy-starts), then re-spawn.
- Dedup is unchanged: at-least-once delivery, dedupe by event id. A cycle killed between the stdout write and the cursor sync may re-emit one line — harmless.
- If you cannot spawn a subagent at all, fall to `route-inline`.
