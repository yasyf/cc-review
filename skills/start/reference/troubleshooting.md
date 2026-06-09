# Troubleshooting

**The binary is missing.** Run `bash "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh"`. It builds `bin/cc-review` from source (needs `bun` and `go` on `PATH`). The SessionStart hook runs this automatically on the first session.

**No comment notifications arrive.** Confirm the Monitor is running (`/tasks`) and that you launched it with `persistent: true`. `cc-review watch` writes one line per event straight to stdout (unbuffered), so as long as the Monitor is armed, each comment becomes a notification. Check the daemon is up with `cc-review status`.

**A flood of comments stopped the Monitor.** Monitors stop themselves under a high event rate. Re-arm the Monitor; `watch` resumes from its cursor, so you miss nothing.

**Edits are blocked.** That's the point: the PreToolUse guard denies edits while a review is open. It lifts once the human presses Submit and the review status becomes `submitted`. If the daemon is down, the guard fails open (edits are allowed).

**The review didn't resume.** Resume is keyed on `(session id, repo root)` — the branch is never part of the key, so a mid-review checkout won't fork it. If the session id differs (e.g. a new session), pass `--resume` to adopt the latest open review for the repo, or `--new` to start fresh.

**Nothing to review.** `start` snapshots the uncommitted working tree (tracked, staged, and untracked, minus ignored) against `HEAD` — or the empty tree when the repo has no commits. With no uncommitted changes, the diff is empty.
