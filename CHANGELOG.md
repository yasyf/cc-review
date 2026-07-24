# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Pin cc-interact v0.24.0 and daemonkit v0.15.0 for the exact fleet-wide runtime hard cut.
- Persisted feedback, stores, and related JSON records now require their exact
  v1 identity, schema fingerprint, and closed field set; legacy or extended
  representations fail closed and are migrated manually at the hard cut.
- `plugin/scripts/install-binary.sh` is rendered from the canonical
  repo-bootstrap template (synced with plugin-template's `render.sh
  --sync-scripts`, provenance-stamped): a brew-installed binary wins, downloads
  land in the durable plugin data dir verified against the release
  `checksums.txt`, and `plugin/bin/cc-review` is only ever a symlink.
- Release builds stamp `version.Version` with the git tag (`{{ .Tag }}`
  instead of `{{ .Version }}`), and `--version` prints exactly that tag — one
  line, no commit suffix. Verbose surfaces keep the `tag (commit)` form via
  `version.String()`.
- The channel no longer pushes a `channel.hello` handshake at attach
  (cc-interact v0.1.5), so attaching to an existing review never wakes an idle
  Claude session. Delivery proof is now solicited: `start` on an
  attached-but-unproven window injects a one-shot, pid-targeted `channel.probe`
  frame — never persisted, so it cannot replay or reach the browser — which lands
  mid-turn where the skill acks it via `channel-ack`. `channel: pending` is the
  normal state until that first ack; `active` still requires the proven round
  trip. The MCP instructions and skill docs drop the handshake language.
- Channel setup now comes from cc-interact's hoisted `channelsetup` package:
  `--apply` no longer writes `~/.claude/settings.json` (that key never fed
  Claude's session channel gate), a machine whose managed settings list the
  plugin but leave `channelsEnabled` false is re-offered (delivery genuinely
  isn't on), and `setup-channels`'s three flags are now mutually exclusive
  instead of silently preferring `--apply`.

### Fixed
- A definitive cc-interact store schema mismatch now archives the wedged
  database and starts fresh instead of crash-looping the daemon.
- The old installer's freshness fast-path compared `--version` output against
  `v$VERSION`, but release builds printed `X.Y.Z (sha)` — stale release
  binaries were never refreshed. The canonical installer compares v-stripped
  and reprovisions stale releases.
- Local dev builds (pseudo-versions like `v0.12.1-0.20260617…+dirty`) matched
  the old installer's `v[0-9]*` arm and were clobbered by a re-download; the
  canonical arm order leaves dev builds alone.
- `plugin.json` version realigned to the latest release (0.19.1) so the
  pinned installer resolves an existing tag.
- The `setup-channels --apply` admin script no longer interpolates paths raw
  into the AppleScript/shell line (command injection via a hostile `$TMPDIR`).
- A wrong-typed `allowedChannelPlugins` in managed settings now errors instead
  of being silently clobbered.
- `vcs` snapshot diffs pass `--no-ext-diff` (via the cc-interact bump), so a
  configured external diff driver no longer corrupts snapshots.

## [0.24.0] - 2026-07-23

### Changed
- Repin cc-interact to v0.19.0. The composed v1 store now rejects any drift in
  the normalized live `sqlite_schema` object set or definitions before the
  daemon starts; there is no migration or repair path.

## [0.18.0]

### Changed
- Release via goreleaser instead of the hand-rolled workflow: the GitHub release
  still carries the bare `cc-review_<os>_<arch>` binaries the plugin downloads, and
  goreleaser now also publishes a Homebrew cask to `yasyf/homebrew-tap`
  (`brew install yasyf/tap/cc-review`). `plugin.json` version is realigned to the
  release tag so install-binary.sh stops re-downloading.

## [0.17.0]

Reviews now feed the cc-family's shared correction memory.

### Added
- On Submit, every inline comment thread is written to cc-transcript's shared
  corrections ledger via `cc-transcript corrections add` (source `cc-review`,
  origin `review`, anchor `review:<reviewID>:<commentID>`). Additive and
  best-effort — a shell-out failure is logged, never strands the frozen feedback.
  The cc-transcript binary is configurable via `CC_TRANSCRIPT_BIN`.
- The session id is persisted onto a review's version so a frozen review can
  anchor its corrections to its session.

### Changed
- The shared decision ledger table is renamed `decisions_v1` → `decisions`
  (vendored DDL byte-identical with cc-transcript v4.1.0). No in-place migration —
  delete `~/.cc-transcript/decisions.db*` and let it rebuild.

## [0.16.0]

The review UI learns the keyboard, and generated files get out of the way.
Lockfiles, vendored code, and generated sources now fold away by default —
peek-expandable, the way GitHub does it — so the diff that matters stands out.

### Added
- Keyboard shortcuts for the review UI: `j`/`k` move between files, `v` marks
  the current file Viewed and advances, `c` collapses or expands it, `n`/`p`
  jump between comments, and `?` opens a shortcuts overlay. A guarded global
  handler yields to text inputs and the Command Deck and never binds Cmd-K; the
  current file is tracked from scroll position and highlighted in place.
- Auto-collapse of generated and vendored files, badged and peek-expandable. A
  new daemon-side detector uses go-enry (a github-linguist port) to find
  unmarked generated/vendored files — lockfiles, minified output, `*.pb.go`,
  `vendor/`, `node_modules/` — and honors `.gitattributes`
  `linguist-generated`/`linguist-vendored` marks via `git check-attr`, where
  `=false` un-marks a file. The flags ride through `files_json` with no schema
  change.

### Changed
- The "Hide generated files" command and the new auto-collapse share one
  server-side definition of "generated"; the old client-side filename regex is
  gone.

## [0.15.0]

The AI bar becomes the Command Deck — a footer that reads the diff and offers
ranked one-tap actions instead of a blank input — and the organize agent streams
a live working phase as it goes. A `--channels` agent also learns in-band that
the channel handshake is status, not a request, so it no longer wakes to an
empty session and asks what to do.

### Added
- The Command Deck replaces the AI bar's blank footer: it reads the current diff
  and surfaces ranked one-tap chips split into an instant lane (file-state ops
  that run client-side and keep working while Claude is disconnected) and a
  Claude lane for semantic asks. Focus or Cmd-K opens an anchored menu with live
  glob preflight, recents, and keyboard nav. Result cards stream the working
  phase, deep-link change rows into the diff, fold in the `awaiting_input`
  answer, and offer one-click undo (server-side for agent edits, client-side for
  instant ops).
- A live working-phase label: the organize agent sets an optional free-text
  `phase` via `update_ai_request` (e.g. "reading 8 files…"), shown live on the
  request card and carried on `ai.request.updated` with no new event type.
- Channel-server instructions. The MCP channel now advertises `instructions` at
  initialize, so every `--channels` session — even one that never ran
  `/cc-review:start` — knows a `channel.hello` or `channel.changed` tag is a
  connection handshake, not a user request. The eager hello payload also carries
  a "system handshake; no reply needed" note as a backstop. (Requires
  cc-interact v0.1.4.)

### Changed
- The Command Deck drops the History popover and the stale-pending clock; the
  daemon already fails stranded requests.
- `ChapterFile.focus` is required on the wire (the daemon always serializes it),
  and the AI bar's answer form now shares the comment thread's Ask-options
  picker.

### Notes
- **Upgrading wipes local review state** (`~/.cc-review`) per the no-migrations
  policy: the `ai_requests` table gained a `phase` column, so the daemon
  recreates the database on its next start.

## [0.14.0]

Reviewers are now guided *within* each file, not just across files: per-file
"what to focus on" hints and per-line marks drive a gutter dot, a hover note,
and a graded dimming of mechanical noise. This release also carries the
organize agent's broader AI-bar powers — risk-batch flips, Claude-authored
annotations, a clarifying-question round-trip, and streamed, fanned-out
organization.

### Added
- Per-line review guidance. The organize agent emits, per file, a `focus` line
  (what to scrutinize and why it carries its risk, distinct from `rationale`'s
  why-here) and a `lines` array of new-side added-line ranges tagged `focus` or
  `mechanical` with a hover note. Focus lines get a gutter dot and a hover
  bubble; a three-tier opacity gradient leaves focus lines at full weight, dims
  untagged changes, and dims mechanical lines most. A "Focus mode" toggle (on by
  default) reverts the diff to full weight.
- `set_file_states_by_risk`: flip every file of a given risk to reviewed/hidden
  in one server-resolved call — the shortcut for "mark all mechanical changes as
  viewed".
- `annotate`: Claude-authored line marks — `highlight` (rendered on the
  attribution decoration path) or `comment` (reuses the comment pipeline,
  excluded from the submit gate so it never blocks the reviewer).
- AI-bar clarifying questions: a request can park on one question
  (`awaiting_input`); the reviewer answers in the AI bar and the request
  redispatches carrying the answer.
- `submit_organization` partial mode: stream chapters as they firm up; the final
  non-partial submit still enforces full file coverage.
- `cc-review:classify-batch` subagent so the organize agent fans a large diff out
  across parallel reviewers instead of bailing.

### Changed
- The organize agent never bails on volume: it trusts its own risk tags, fans
  out, streams every batch, and asks only on genuine intent ambiguity.
- The file tree sidebar dims and strikes through reviewed files.

### Notes
- **Upgrading wipes local review state** (`~/.cc-review`) per the no-migrations
  policy. The schema gained the annotations table and `ai_requests`
  question/answer/attempt columns; the daemon recreates the database on its next
  start. (Per-line focus data itself needs no schema change — it rides the
  existing organization JSON.)

## [0.13.0]

Event delivery is now a single decision tree with one reference file per route,
the no-Monitor fallback no longer idles or blocks organize dispatch, and
`get_review_files` can no longer overflow the tool-result limit on a large review.

### Added
- `cc-review watch --once` exits after the first event, so a background relay can
  deliver one event and relaunch from its cursor — replacing the fifo exit-per-event
  hack. (Requires cc-interact v0.1.2.)
- Event delivery is a decision tree (step 3 of `/cc-review:start`): channel tags, a
  Monitor, an agent-teams streamer teammate, a one-shot streamer subagent, or an
  inline watcher — each documented in its own `reference/route-*.md`.

### Changed
- `get_review_files` writes the full file list (`review_files_path`, JSONL) and the
  organization (`organization_path`, JSON) to disk and inlines only a small or
  filtered subset, so a large review never overflows the tool-result cap. New
  `status`/`reviewed`/`hidden` filters narrow the inline subset.
- On the agent-teams route, organize work runs as a `cc-review:organize` teammate
  rather than a background agent — an in-process team forbids the lead from spawning
  background agents.

### Fixed
- The Monitor-less fallback no longer goes idle or hits the Bash 10-minute timeout:
  the streamer relays via its completion result instead of a resident teammate,
  keeping the lead free to dispatch background organize agents.

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

[Unreleased]: https://github.com/yasyf/cc-review/compare/v0.24.0...main
[0.24.0]: https://github.com/yasyf/cc-review/compare/v0.23.0...v0.24.0
[0.6.0]: https://github.com/yasyf/cc-review/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/yasyf/cc-review/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/yasyf/cc-review/compare/v0.2.0...v0.4.0
[0.2.0]: https://github.com/yasyf/cc-review/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/yasyf/cc-review/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/yasyf/cc-review/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/yasyf/cc-review/releases/tag/v0.1.0
