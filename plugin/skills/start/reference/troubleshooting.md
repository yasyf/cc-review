# Troubleshooting

**The binary is missing.** Run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"`. It downloads the release binary for this platform into `bin/cc-review`. The SessionStart hook runs this automatically on the first session.

**No comment notifications arrive.** Confirm the Monitor is running (`/tasks`) and that you launched it with `persistent: true`. `cc-review watch` writes one line per event straight to stdout (unbuffered), so as long as the Monitor is armed, each comment becomes a notification. Check the daemon is up with `cc-review status`.

**`channel: pending` printed / tags never arrive.** `pending` is wired-but-unproven: the channel server is attached, but Claude Code may be silently dropping its notifications. The Monitor is the route — arm it exactly as on `inactive`. On the first real `<channel source="cc-review">` tag, run `cc-review channel-ack --session "$CLAUDE_CODE_SESSION_ID" --cwd "$PWD"` so future starts in this window print `active`. Sessions launched without `--channels`, or where Claude Code prints *"--channels ignored"* / *"Channels are not currently available"*, correctly stay `pending` or `inactive`.

**A flood of comments stopped the Monitor.** Monitors stop themselves under a high event rate. Re-arm the Monitor; `watch` resumes from its cursor, so you miss nothing.

**Events replayed after re-arming a Monitor.** Delivery is at-least-once: a Monitor re-armed in a later session can replay events you already handled as channel tags. Dedupe by event id and treat already-handled events as informational — replies dedupe server-side, and organize dispatch dedupes by request id.

**Edits are blocked.** That's the point: the PreToolUse guard denies edits while a review is open. It lifts once the human presses Submit and the review status becomes `submitted`. If the daemon is down, the guard fails open (edits are allowed).

**The review didn't resume.** Resume follows the Claude *window*: `/clear` and resume in the same window always pick the review back up (the branch is never part of the key, so a mid-review checkout won't fork it either). A *different* window only adopts a review whose owning window has exited; while the owner is alive, a second window's `start` creates its own separate review. Pass `--new` if you wanted a fresh review instead.

**The stream went quiet after a plugin upgrade.** Upgrading replaces the daemon mid-session; streams refresh their connection automatically, but a watcher built by the old binary may stop. Re-arm the Monitor — `watch` resumes from its cursor, so nothing is lost.

**Nothing to review.** In a git repo, `start` snapshots the uncommitted working tree (tracked, staged, and untracked, minus ignored) against `HEAD` — or the empty tree when the repo has no commits. In a jj repo (including colocated), it snapshots the working-copy change (`@`) against its parent. With no changes, the diff is empty.
