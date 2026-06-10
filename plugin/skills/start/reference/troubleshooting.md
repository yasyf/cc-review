# Troubleshooting

**The binary is missing.** Run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"`. It downloads the release binary for this platform into `bin/cc-review`. The SessionStart hook runs this automatically on the first session.

**No comment notifications arrive.** Confirm the Monitor is running (`/tasks`) and that you launched it with `persistent: true`. `cc-review watch` writes one line per event straight to stdout (unbuffered), so as long as the Monitor is armed, each comment becomes a notification. Check the daemon is up with `cc-review status`.

**A flood of comments stopped the Monitor.** Monitors stop themselves under a high event rate. Re-arm the Monitor; `watch` resumes from its cursor, so you miss nothing.

**Edits are blocked.** That's the point: the PreToolUse guard denies edits while a review is open. It lifts once the human presses Submit and the review status becomes `submitted`. If the daemon is down, the guard fails open (edits are allowed).

**The review didn't resume.** Resume is automatic: `start` adopts the repo's latest open review even from a new or rotated session (the branch is never part of the key, so a mid-review checkout won't fork it either). Pass `--new` if you wanted a fresh review instead. Note that a *submitted* review doesn't resume across sessions — only open ones do.

**The stream went quiet after a plugin upgrade.** Upgrading replaces the daemon mid-session; streams refresh their connection automatically, but a watcher built by the old binary may stop. Re-arm the Monitor — `watch` resumes from its cursor, so nothing is lost.

**Nothing to review.** `start` snapshots the uncommitted working tree (tracked, staged, and untracked, minus ignored) against `HEAD` — or the empty tree when the repo has no commits. With no uncommitted changes, the diff is empty.
