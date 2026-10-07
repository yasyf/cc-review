---
title: Internals
description: How the cc-review daemon, event log, storage, and SPA fit together — for contributors and integrators.
aliases:
  - /internals/
---

cc-review is one Go binary. Every user-facing command in the [CLI reference](cli-reference.md) talks to a background daemon over a unix socket, and that same daemon serves the web UI over HTTP on 127.0.0.1. This page explains how those pieces fit together.

## Daemon lifecycle

The daemon needs no separate install step: daemonkit runs it as a `LaunchAgent` on macOS and under `cc-review supervise` on Linux. Every user-facing CLI command calls `daemon.EnsureCurrent` before doing work. If no daemon at the current binary version answers on the socket, the CLI spawns a detached `cc-review daemon` and waits for it to come up. The `daemon` subcommand is a hidden cobra command in `internal/cli/hidden.go`.

Cold starts are serialized by an exclusive flock on `~/.cc-review/v1/locks/start.lock`, so concurrent commands racing to boot the daemon produce exactly one process. The SessionStart hook uses `EnsureCurrentIfRunning` instead, which upgrades a running daemon without ever booting one, because a hook must not be the thing that starts daemons. The edit-guard hook skips the handshake entirely: it talks to whatever daemon answers the socket and fails open when none does.

Version skew resolves newest-wins on first contact. When a CLI finds a daemon built from a strictly older binary, the freshly spawned daemon's `listen()` evicts the holder: it asks the old daemon to shut down over the socket, escalates to SIGKILL if it wedges, and waits for the old process to exit before binding. A same-or-newer holder is never evicted — the spawned daemon exits with an error instead, and the spawning client accepts the running daemon. The tie refusal is what prevents two daemons from evicting each other in a loop, and the newer-holder refusal is what stops sessions pinned to older plugin builds from tearing down the shared daemon every turn. A dev build counts as newest: a dev daemon is never evicted, and a dev binary always takes over a release daemon.

The HTTP plane binds a 127.0.0.1 port and publishes it to `~/.cc-review/v1/http.json` so the CLI and stream consumers can find it. The file is left in place on shutdown, and a booting daemon tries that previous port first before falling back to an ephemeral one, so printed review URLs survive a daemon swap. `CC_REVIEW_HTTP_PORT` overrides that port selection, and `CC_REVIEW_URL` replaces the origin of printed review URLs; the daemon reads both variables, so on Linux set them on the `cc-review supervise` process. With `cc-review daemon --dev` the port is pinned to 8787, overriding `CC_REVIEW_HTTP_PORT`. The Vite dev proxy expects the API there during frontend work.

When synckit's mesh state exists, the daemon also serves the HTTP plane on its own tailnet addresses, one extra listener per address on a shared port that survives restarts. Each listener answers both plaintext and TLS, using a certificate from `tailscale cert` for the machine's MagicDNS name when the tailnet publishes one. A reconcile pass every 30 seconds binds addresses that appeared after boot, so a late `tailscale up` needs no restart. `start` and `github setup` print these addresses as `tailnet:` URLs: https on the certificate's name once it is minted, otherwise http on the bare machine name.

Daemons spawned by the CLI append their stdout and stderr to `~/.cc-review/v1/daemon.log` — boot lines, eviction sequences, and panics all land there across daemon generations. A manual `cc-review daemon` run keeps its output on the terminal.

## Two planes

The daemon exposes two surfaces.

The **control plane** is a unix socket at `~/.cc-review/v1/daemon.sock` (mode 0600) speaking exact protocol v1. This is what CLI commands call. The ops dispatched in `internal/daemon` are `health`, `shutdown`, `start`, `resolve`, `reply`, `feedback`, `status`, `session-record`, `guard-edit`, `file-states`, `update-ai-request`, `submit-organization`, and `review-files`. A mismatched protocol is rejected before dispatch.

The **HTTP plane** (`internal/httpapi`) binds 127.0.0.1. Loopback requests pass without credentials; a tailnet request passes only when synckit trusts the peer's address, and a browser request's Origin must name loopback or the daemon's own MagicDNS name or tailnet IPs, never another machine. It serves the embedded SPA at `/`, a JSON REST surface, and one SSE stream. These routes are registered in `internal/httpapi/server.go`.

```
GET  /api/session/{reviewId}
GET  /api/session/{reviewId}/versions
GET  /api/reviews
POST /api/comments
PUT  /api/comments/{id}
POST /api/comments/{id}/retry
POST /api/replies/{commentId}
POST /api/file-states
POST /api/ai-requests
POST /api/ai-requests/{id}/undo
POST /api/submit
GET  /events
GET  /github/setup
GET  /github/setup/callback
```

`/github/setup` serves the GitHub App manifest form behind `cc-review github setup`. The manifest's redirect names the scheme and host the page was opened on, so a setup started over the tailnet returns there. Its callback sits outside the daemon's auth guard so GitHub's redirect can reach it; a single-use state nonce with a 10-minute lifetime protects it instead.

## The event log

All realtime behavior rides on one append-only `events` table, keyed `(review_id, seq)`. The daemon's `AppendEvent` is the single chokepoint: it persists the row, then publishes a wakeup on an in-memory bus so parked SSE handlers re-read the log.

Delivery is at-least-once. `GET /events?session=<ref>` streams a review's log; consumers resume from their last sequence number via `Last-Event-ID`, or via the `?last_event_id=` query fallback since native `EventSource` cannot set headers on the initial request. The Claude-side consumers, `watch` and the MCP channel server, persist their cursor on disk per consumer, so a restart resumes without re-delivering.

Each event carries an `origin` of `human`, `agent`, or `system`. The browser subscribes with no filter and sees everything; Claude-side consumers pass `exclude_origin=agent` so they never receive an echo of their own replies. The origin is separate from a comment's `author` (`user`, `claude`, `remote`, or `automation`). In a PR review, the poller maps GitHub authors onto both. The viewer is `user` with origin `human`, and a coworker is `remote` with origin `human`. The user's cc-review App bot is `claude` with origin `agent`, which is what keeps Claude's own GitHub comments from looping back to it. Duplicate suppression on the write side uses an optional `dedup_key` with a unique partial index, so a redelivered reply inserts once and re-emits nothing.

Graphite's stack comment is `automation` with origin `agent` whoever posted it. Browser-only events use origin `agent` too: `pr.updated`, `comment.synced`, and the `comment.created` events of a PR's first poll, which imports the comments already on GitHub. That poll queues one `pr.imported` summary, with origin `human` only when it lists open line threads from people, and the summary doubles as the record that the PR was imported.

Named consumers also register presence: their attach and detach transitions drive `channel.changed` events, which is how the UI knows whether a live Claude session is wired to the review. `channel.changed` is delivered to the browser only — named consumer streams (`channel`, `watch`) filter it out, since a consumer learning about its own attachment is noise.

Presence alone never proves delivery: Claude Code silently drops channel notifications when channels are unavailable, so `channel: active` requires the model to have acknowledged a delivered tag via `channel-ack`. A `start` on an attached-but-unproven window solicits that proof by injecting a one-shot `channel.probe` frame into exactly that window's channel stream. The probe bypasses the event log and carries no SSE id — it cannot replay on reconnect, and neither the browser nor `watch` ever sees it. It lands while the model is mid-turn running the start skill, so no idle session is ever woken.

## Storage

State lives in a single SQLite database via `modernc.org/sqlite` (pure Go, no cgo). `cc-interact/store` owns the single-writer connection, WAL journaling, busy timeout, core tables, and exact schema fingerprint; `internal/store` contributes cc-review's declarative schema and domain CRUD.

The database carries an exact v1 schema marker and fingerprint. There are no migrations: a future schema epoch uses a fresh namespace, while the derived `~/.cc-review/v1` tree can be discarded and rebuilt. Large patches stay out of the database; a version row stores only the patch path and a files summary.

## State directory layout

`internal/paths` owns the layout under `~/.cc-review/v1`. The directory is always under the home directory; `CLAUDE_CONFIG_DIR` does not move it.

```
~/.cc-review/v1/
├── daemon.sock             # control-plane unix socket
├── daemon.log              # spawned daemons append stdout/stderr here
├── http.json               # HTTP port handshake, kept across restarts for port reuse
├── channels-setup.json     # marker: the one-time channels offer was made
├── github-app.json         # the cc-review GitHub App's id, slug, and bot login (key in the Keychain)
├── repos/<owner>/<name>.git  # blobless thin store per PR-review repo
├── locks/
│   └── start.lock          # flock serializing lazy daemon starts
├── cc-interact-v1/
│   ├── state.db            # exact core + review + turn schema
│   └── subjects/<review-id>/
│       ├── watch.cursor
│       └── channel.cursor
└── subjects/<review-id>/
    ├── snap_N.patch        # unified patch for version N
    └── feedback_N.json     # frozen feedback for version N (written on Submit)
```

## Working-tree snapshots

cc-interact's `vcs` package turns a working copy's pending changes into a git-format patch. Detection walks upward from the cwd without spawning a subprocess. A `.jj` directory means jj; a `.git` entry means git, and the check accepts both a directory and a file since git worktrees use a file. In a colocated repo jj wins. Git diffs a dirty tree against `HEAD`, a clean tree against the fork point from trunk, and a fresh repo against the empty tree; jj diffs against the working-copy parent.

A PR review never reads the working copy. `internal/prstack` resolves the stack from GitHub, and `internal/thinstore` keeps one blobless bare clone per repo under `repos/`, fetching each PR's `refs/pull/<N>/head` and its merge-base commit at depth 1. cc-interact's `vcs.DiffRange` then diffs each section, and git lazily fetches only the blobs of changed files.

Each `start` captures a new snapshot and inserts a new version row, writing the patch to a temp file first and renaming it into place so a write failure can never leave a committed-but-unreadable version. Per-file fingerprints let reviewed marks survive across versions: a file stays marked reviewed exactly while its diff content is unchanged.

## The embedded SPA

The frontend is a Vite + React app in `web/`, built into `internal/web/dist` and compiled into the binary with `go:embed`. Because the embed happens at compile time, the web build must run before the Go build:

```sh
cd web && bunx vite build
cd .. && go build ./cmd/cc-review
```

A committed placeholder `index.html` keeps a clean tree compiling; a real build replaces it with hashed assets. The HTTP plane registers the SPA handler last and least-specific, so `/api` and `/events` always win routing.
