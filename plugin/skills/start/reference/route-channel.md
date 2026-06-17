# Route: channel tags

`channel: active` (step 1) — this window's channel is proven and pushes each review event straight into your context as a `<channel source="cc-review" …>` tag carrying the same JSON payload the other routes stream. You arm **nothing**: no Monitor, no streamer, no `watch`. Arming one would double every event.

## Receive

Each `<channel>` tag is one event. React to it exactly as skill step 4 — `Read` the file under a `comment.created`/`comment.updated`, dispatch a `cc-review:organize` agent for `ai.request.created`, treat the rest as informational, and end on `submit` (step 5). You are **not** in a team on this route, so organize is a background **Agent** (`subagent_type: "cc-review:organize"`, `run_in_background: true`, full model).

## Write back

The channel exposes the same five tools the CLI does — `reply`, `set_file_states`, `update_ai_request`, `submit_organization`, `get_review_files`. Use them (or the `cc-review` CLI) to post clarifying questions and drive organize work.

## Edges

- Delivery is fire-and-forget at turn boundaries and can drop silently — the same at-least-once cursor/idempotency rules as every route apply (dedupe by event id; replies dedupe server-side, organize dispatch dedupes by request id).
- A re-armed watcher in a later session may replay events you already saw as tags; treat already-handled events as informational.
- Approval and the `--channels` launch flag: `reference/channels-setup.md`.
