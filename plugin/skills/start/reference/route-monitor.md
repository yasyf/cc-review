# Route: Monitor on `watch`

You have a **Monitor** tool and the channel isn't active. Stream `watch` under a persistent Monitor: it runs one long-lived `watch` process and turns each line it prints into a chat notification, so you never block and never poll.

## Arm

Launch a **Monitor** with `persistent: true`, description `cc-review comments`, wrapping (note: **no** `--once` here — the Monitor wants the continuous stream):

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" watch --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

Each printed line is one JSON event → one notification. Tell the user you're watching and keep working.

## React

Per skill step 4. You are **not** in a team on this route, so dispatch organize as a background **Agent** (`subagent_type: "cc-review:organize"`, `run_in_background: true`, full model). The Monitor's **final** line is the `submit` event — the `watch` process exits on it — so that line sends you to step 5.

## Channel upgrade

If a `<channel source="cc-review">` tag arrives, channels went live: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, **TaskStop** the Monitor, and switch to `route-channel`. Dedupe the overlap by event id.

## Edges

- Monitor line buffering, daemon lazy-start, and resume keying: `reference/troubleshooting.md`.
