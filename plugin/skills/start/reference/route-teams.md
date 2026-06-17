# Route: agent teams (streamer teammate)

You have a **SendMessage** tool — agent teams are enabled (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`) and you, the main session, are the team **lead**. No Monitor, no active channel. Relay `watch` through a **streamer teammate**, and run organize work through **organize teammates** — an in-process team forbids the lead from spawning `run_in_background` agents, so teammates are how you do async work on this route.

## Spawn the streamer teammate

Use the **Agent** tool with `model: "haiku"` (it only runs a command and relays one line) and this prompt — substitute the session id and repo root:

> You are the cc-review event streamer. Loop: run the command below as a `run_in_background` Bash task and await its exit — it prints exactly one JSON event line. `SendMessage({to:"main"})` that raw line. If its `type` is `submit`, send it, then stop. Otherwise loop again. Never run it in the foreground — a quiet stretch would hit the 10-minute Bash timeout and end your turn. Never edit code or reply to comments; you only relay raw lines.
>
> ```sh
> "${CLAUDE_PLUGIN_ROOT}/bin/cc-review" watch --once --session "<SESSION>" --cwd "<CWD>"
> ```

`watch --once` exits the instant one event arrives and resumes from its cursor on relaunch, so the loop loses nothing and stays armed between events with no polling. Background **Bash** is allowed for an in-process teammate — only background **agents** are blocked.

## React (you, the lead)

Each event reaches you as a streamer **message** — read it from the message, not the teammate's `.output` file (that floods your context). React per skill step 4. For `ai.request.created` (and risk re-ranks) spawn a **`cc-review:organize` teammate** — the **Agent** tool, `subagent_type: "cc-review:organize"`, full model, the request JSON as the prompt — **not** a `run_in_background` agent (the lead can't spawn one while teammates exist). The teammate does the work and closes its own request; you keep handling comments concurrently.

## Submit

A relayed line whose `type` is `submit` ends it: the streamer teammate stops itself, and you drain open questions (step 5). Shut the streamer teammate down.

## Channel upgrade

If a `<channel source="cc-review">` tag arrives, channels went live: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, shut the streamer teammate down, and switch to `route-channel`.

## Edges

- Dedup is unchanged: at-least-once delivery, dedupe by event id (replies dedupe server-side, organize dispatch dedupes by request id).
- `/resume` does not restore in-process teammates — re-running `/cc-review:start` re-establishes the streamer.
