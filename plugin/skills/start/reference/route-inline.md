# Route: inline `watch --once` (floor)

No Monitor, no active channel, no agent teams, and **no Agent tool** — so there is no subagent to relay through. Run the watcher yourself, in this thread, as a `run_in_background` Bash task.

## Loop

Run, re-launched per event:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" watch --once --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"
```

A `run_in_background` Bash task re-invokes you when it exits, and `watch --once` exits the instant one event arrives (resuming from its cursor next launch) — so you get one clean event line per relaunch, no 10-minute foreground timeout, no polling. React per skill step 4, then relaunch. On `submit`, stop relaunching and drain open questions (step 5).

## Organize

With no Agent tool you cannot dispatch a `cc-review:organize` subagent; tell the user, and handle `ai.request.created` inline as best you can (or defer it until a richer session resumes). This route is the headless/degenerate floor — interactive Monitor- or teams-capable sessions never reach it.

## Channel upgrade

If a `<channel source="cc-review">` tag arrives, channels went live: run `"${CLAUDE_PLUGIN_ROOT}/bin/cc-review" channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"`, stop relaunching, and switch to `route-channel`.
