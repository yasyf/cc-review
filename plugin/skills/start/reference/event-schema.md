# Event schema

`cc-review watch` prints one JSON object per line. Each carries a `type` and a `version_number`. Events whose origin is your own replies are filtered out, so the echo never loops back. The browser receives the same stream plus your replies.

| `type` | Payload fields | You act on it? |
| --- | --- | --- |
| `comment.created` | `commentId`, `comment` (`filePath`, `side`, `range`, `lineContent`, `body`, `status`, `replies`) | Yes — `Read` the file for context, then optionally `reply`. |
| `comment.updated` | `commentId`, `comment` | Yes — the human edited a comment or answered an ask in the web UI: the thread's ask reply gains `answered: true` and `askAnswer` (`{selected, other?, notes?}`). This is the only delivery of a web ask-answer — no separate answer reply is created. |
| `comment.resolved` | `commentId` | Informational. |
| `submit` | `feedbackPath` | Yes — stop reacting, run `feedback`, drain open questions. |
| `status.changed` | `status` | Informational. |
| `notification` | `level`, `message` | Informational. |

Your own replies surface in the browser (and the frozen feedback) as `claude.question`, `claude.ask`, and `claude.clarification`, threaded under the comment they answer. A `claude.ask` payload is `{commentId, reply}`, the reply carrying `ask: {header, multiSelect, options: [{label, description?, preview?}]}`. You never receive these back from `watch`.

Delivery is at-least-once: `watch` resumes from a persisted cursor, so a restart re-delivers nothing already emitted, and `reply` is idempotent, so a re-delivered comment can't double-post.
