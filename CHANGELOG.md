# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.12.0]

AI-bar requests submitted while no Claude session is attached are no longer
lost, and the event-delivery path is hardened against leaked watchers and
windows whose channel never proves out.

### Added
- Monitor-less event delivery: when the Claude window has no Monitor tool,
  `/cc-review:start` streams review events through a background streamer
  subagent instead. See `reference/monitor-fallback.md`.
- The channel server pushes a hello tag at attach, so a window's channel can
  prove `active` without waiting for the first human comment.

### Fixed
- Orphaned AI-bar requests recover: `/cc-review:start` re-offers every request
  still open on the current version — the system organize plus any human
  AI-bar prompts that arrived while no session was attached — so a freshly
  attached session dispatches each. A request left pending past ten minutes is
  failed by the daemon, so the UI never shows a permanently stuck "queued" chip.
- `cc-review watch` exits when its Claude window dies, so a leaked watcher no
  longer holds the shared event cursor or drains undelivered events; stream
  cursor writes now fail loud instead of silently replaying the backlog later.
- The reorganizing banner is scoped to system organize requests only.

## [0.11.0]

Ends the daemon version war: concurrent sessions pinned to different cached
plugin versions no longer kill and respawn the shared daemon on every turn
boundary.

### Fixed
- Newest-wins eviction: only a strictly newer binary replaces a running
  daemon; older clients accept a newer daemon instead of evicting it. Dev
  builds rank newest, so `task dev-daemon` still takes over a release daemon.
- The daemon rebinds its previously published HTTP port when free, and no
  longer deletes `http.json` on shutdown — review URLs survive daemon swaps,
  upgrades, and crashes.
- `channel.changed` connectivity flips are delivered to the browser only
  (the Claude-connected dot); the `channel` and `watch` consumer streams
  filter them, so they never reach the agent's context.

### Added
- `~/.cc-review/daemon.log`: lazily spawned daemons append stdout/stderr
  there, so a daemon death is diagnosable after the fact.

## [0.10.0]

cc-review joins the cc-family session-activity platform. **Upgrading wipes
local review state** (`~/.cc-review`) per the no-migrations policy.

### Added
- Gate decisions are durable: every guard-edit verdict writes an
  `allow`/`block` row (tool name + RFC 8785 content digest) to the shared
  cc-family ledger at `~/.cc-transcript/decisions.db`, dual-written with
  captain-hook and readable by any miner.
- Bypass detection by subtraction: a locked-review turn whose tree changed
  beyond what gate-allowed tool calls and slice-visible Bash calls explain
  lands a `bypass-detected` note with `changed_files`/`attributed_files`.
- `cc-review export activity --session <uuid>`: the `cc-review.activity/1`
  JSON contract (integer-ms turns, version-dimensioned attributions) consumed
  by cc-transcript's Python reader.
- `GET /api/turns/{id}/provenance` + an Activity sidebar tab: per-turn
  hook/gate decision rows with action chips, and lazy tool-call provenance
  fetched via `cc-transcript slice` (degrades cleanly when absent).
- `internal/digest`: the cross-language tool digest (RFC 8785 + sha256),
  conformance-gated by the fixture corpus generated from cc-transcript.

### Removed
- `turns.transcript_path` / `turns.transcript_offset` (written, never read) —
  turn provenance is keyed by session UUID + time window.

## [Unreleased]

## [0.9.0] - 2026-06-12

### Changed
- The one-time channel-approval offer no longer requires the session to
  descend from a `--dangerously-load-development-channels` launch. `start`'s
  `setup:` line (and `setup-channels --check`) offers whenever no marker
  exists and the channel is not yet approved, so release installs and
  Monitor-only sessions are asked too.

## [0.8.0] - 2026-06-11

### Changed
- `start`'s channel line is three-state: `channel: pending` until this window's
  channel is proven — the first delivered `<channel>` tag acknowledged via the
  hidden `cc-review channel-ack` — `active` only afterward, and `inactive` with
  no channel consumer. Fixes the false `channel: active` when Claude Code
  rejects channels, which skipped the Monitor and left no event route at all.

### Fixed
- An unchanged resume no longer strands an open organize request from a dead
  session: the still-open request is re-offered in `start`'s `organize:` line
  (same id), or closed when the latest version is already organized — clearing
  the stuck QUEUED chip.

## [0.7.1] - 2026-06-11

### Fixed
- Adding an inline comment no longer leaves a second draft composer holding
  the same text below the new thread. The composer previously waited for a
  mutate-level callback to close, but the `comment.created` SSE event shifts
  its index-keyed annotation portal and remounts it, dropping that callback.
  It now closes eagerly on submit, like the reply box.

## [0.7.0] - 2026-06-11

### Added
- Per-turn change attribution: `UserPromptSubmit`/`Stop` plugin hooks record
  every Claude turn as a pair of working-tree snapshots, using a persistent
  scratch index and private object dir for git and native working-copy
  snapshotting for jj. Each review version maps its added lines to the turn
  that wrote them; manual edits between turns stay untagged.
- The diff UI draws a colored attribution strip per turn, with a toolbar
  legend and a hover popover showing the turn's prompt excerpt and timestamp.
  Clicking a legend chip focuses that turn and dims the rest.
- The session payload gains `turns` and `attributions`. Turn rows store light
  pointers (tree OIDs and a transcript path plus byte offset) with a capped
  inline prompt excerpt; per-repo snapshot scratch state sweeps itself once no
  turn remains inside the 14-day attribution window.
- Stack-ranked living todo view replaces the risk mode: chapters rank by
  remaining work, with rank semantics and re-rank dispatch for the organize
  agent.

The new `turns` and `turn_attributions` tables are additive; restarting the
daemon (`cc-review stop`) applies them to an existing `~/.cc-review`.

## [0.6.0] - 2026-06-11

### Added
- The daemon carries the chapter organization forward when a new version's
  per-file diff fingerprints exactly match the last organized one: chapters
  appear instantly, stranded organize requests close, and no organize agent
  is dispatched.
- `get_review_files` returns the latest organization annotated per file
  (`changed`/`moved`/`removed`; absent = unchanged) plus `new_paths`, so the
  organize agent rebuilds incrementally and AI-bar reorganizes see the live
  organization.
- Per-review pinned diff base with trunk fallback: fingerprints stay stable
  across mid-review commits.
- Starlight docs site with UI screenshots, deployed to GitHub Pages; README
  rewritten as a front door.

### Changed
- Plugin renamed from `review` to `cc-review`: the skill is now `/cc-review:start`,
  the channel id is `plugin:cc-review@cc-review`, and the MCP tool prefix is
  `mcp__plugin_cc-review_cc-review__*`. No more collision with the builtin
  `/review` skill.
- `cc-review start` eagerly prints `setup:` and `organize:` lines, so the skill
  no longer spends a second turn on `setup-channels --check`.
- Organize work moved out of the `/review:organize` skill into a
  `cc-review:organize` plugin agent dispatched in the background, keeping the
  main session free of chapter-building context.
- File-change fingerprints hash file modes, so a `chmod`-only edit counts as
  a change for review-state and organization carry.

Migration: `/plugin uninstall review@cc-review`, then
`/plugin install cc-review@cc-review`, relaunch with
`plugin:cc-review@cc-review`, and `rm -rf ~/.cc-review` — the stale
managed-settings entry and marker would otherwise suppress the setup re-offer.

## [0.5.0] - 2026-06-10

### Added
- Managed reviews: per-file reviewed/hidden states, AI requests from the web
  UI's AI bar, and chapter organization with per-file risk.

## [0.4.0] - 2026-06-10

### Added
- Per-window review ownership and jj snapshot support behind auto-detection.

## [0.2.0] - 2026-06-10

### Added
- Reviews follow the human across Claude session rotation: the SessionStart hook
  and `start` reparent the repo's open review to the current session, with the
  full binding history in a new append-only `review_sessions` table. The edit
  guard, `feedback`, and `status` keep working after a resume/continue.
- Version-skew eviction: every user-facing command verifies the daemon's build
  version and replaces a stale daemon (graceful shutdown, then SIGKILL of the
  exact socket peer). Live event streams refresh their connection and survive
  the upgrade.
- `start` prints `channel: active|inactive`; the skill skips the Monitor when
  the MCP channel is already streaming the review, ending double delivery.

### Removed
- `cc-review start --resume` — adopting the repo's open review is now the
  default; `--new` still forces a fresh review.

## [0.1.2] - 2026-06-10

### Fixed
- Channel server resolves the repo's open review even when spawned under a
  sibling session id.

## [0.1.1] - 2026-06-10

### Fixed
- Declare `claude/channel` under `capabilities.experimental` so Claude Code
  registers the channel.

## [0.1.0] - 2026-06-10

### Added
- Initial release: `/review:start` skill, PR-like web UI, Monitor + MCP channel
  streaming, append-only SQLite history, edit guard, release-asset binaries.

[Unreleased]: https://github.com/yasyf/cc-review/compare/v0.6.0...main
[0.6.0]: https://github.com/yasyf/cc-review/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/yasyf/cc-review/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/yasyf/cc-review/compare/v0.2.0...v0.4.0
[0.2.0]: https://github.com/yasyf/cc-review/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/yasyf/cc-review/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/yasyf/cc-review/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/yasyf/cc-review/releases/tag/v0.1.0
