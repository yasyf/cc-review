# Event schema

`cc-review watch` prints one JSON object per line. Each carries a `type` and a `version_number`. Events whose origin is your own replies or MCP tool calls are filtered out, so the echo never loops back. The browser receives the same stream plus your replies.

| `type` | Payload fields | You act on it? |
| --- | --- | --- |
| `comment.created` | `commentId`, `comment` (`filePath`, `side`, `range`, `lineContent`, `body`, `status`, `replies`) | Yes — `Read` the file for context, then optionally `reply`. |
| `comment.updated` | `commentId`, `comment` | Yes — the human edited a comment or answered an ask in the web UI: the thread's ask reply gains `answered: true` and `askAnswer` (`{selected, other?, notes?}`). This is the only delivery of a web ask-answer — no separate answer reply is created. |
| `comment.resolved` | `commentId` | Informational. |
| `submit` | `feedbackPath` | Yes — stop reacting, run `feedback`, drain open questions. |
| `status.changed` | `status` (`"expired"` \| `"closed"`) | Informational — the review left the open state without a submit: `expired` when it sat idle for 24 hours, `closed` when the human closed it without submitting. No `submit` follows. |
| `notification` | `level`, `message` | Informational. |
| `ai.request.created` | `request` (`id`, `source`, `prompt`, `status`, `attempt`, `summary`, `unmatched`, `changes`, `question?`, `answer?`, `createdAt`, `updatedAt`) | Yes — dispatch the `cc-review:organize` agent in the background (Agent tool, `run_in_background: true`) with the `request` JSON as its prompt — unless its `(id, attempt)` matches one already dispatched (the same request redelivered): ignore it. Dedupe by `(id, attempt)`; a higher `attempt` for a known id is the human's answer to a clarifying question (`status: "answered"`, carrying `question` + `answer`) — dispatch it. `source: "system"` is the daemon's auto-organize request; `source: "user"` came from the AI bar. |
| `ai.request.updated` | `request` | Informational — a human undo (`status: "undone"`), the daemon closing a carried-forward organize request (`status: "done"`), or the organize agent parking on a clarifying question (`status: "awaiting_input"`, carrying `question`); the browser renders the question card. The organize agent's own `update_ai_request` calls are filtered out. |
| `file.states` | `states` (`[{path, reviewed, hidden, reason?}]`), `aiRequestId?`, `undoOf?` | Informational — the human's checkboxes, an undo, or the daemon unmarking changed files. Values are absolute per path, never deltas. |
| `organization.updated` | `organization` (`overview`, `chapters`) | Never delivered to you — it originates from the organize agent's `submit_organization` (or the daemon carrying the organization forward onto an identical new version); the browser renders it. |
| `annotations.updated` | `annotations` (`[{id, filePath, side, start, end, label}]`) | Never delivered to you — Claude-authored line highlights from the organize agent's `annotate`; the browser renders them on the diff. |
| `version.created` | *(none beyond `type`, `version_number`)* | Informational — a new version was captured. |
| `channel.changed` | `connected` | Never delivered to you — drives the browser's Claude-connected indicator. |
| `channel.probe` | `note` | Yes — run `channel-ack`, reply nothing. Arrives only as a channel tag (never via `watch`), solicited by your own `start` while the channel is attached but unproven; never persisted, so no Monitor or reconnect can replay it. |

Your own replies surface in the browser (and the frozen feedback) as `claude.question`, `claude.ask`, and `claude.clarification`, threaded under the comment they answer. A `claude.ask` payload is `{commentId, reply}`, the reply carrying `ask: {header, multiSelect, options: [{label, description?, preview?}]}`. You never receive these back from `watch`.

Delivery is at-least-once: `watch` resumes from a persisted cursor, so a restart re-delivers nothing already emitted, and `reply` is idempotent, so a re-delivered comment can't double-post.
