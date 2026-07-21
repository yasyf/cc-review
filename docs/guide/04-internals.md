---
title: Internals
description: How the cc-review daemon, event log, storage, and SPA fit together — for contributors and integrators.
aliases:
  - /internals/
---

cc-review is one Go binary. Every user-facing command in the [CLI reference](cli-reference.md) talks to a background daemon over a unix socket, and that same daemon serves the web UI over HTTP on 127.0.0.1. This page explains how those pieces fit together.

## Daemon lifecycle

There is no install step for the daemon and no service manager. Every user-facing CLI command calls `daemon.EnsureCurrent` before doing work. If no daemon at the current binary version answers on the socket, the CLI spawns a detached `cc-review daemon` and waits for it to come up. The `daemon` subcommand is a hidden cobra command in `internal/cli/hidden.go`.

Cold starts are serialized by an exclusive flock on `~/.cc-review/v1/locks/start.lock`, so concurrent commands racing to boot the daemon produce exactly one process. The SessionStart hook uses `EnsureCurrentIfRunning` instead, which upgrades a running daemon without ever booting one, because a hook must not be the thing that starts daemons. The edit-guard hook skips the handshake entirely: it talks to whatever daemon answers the socket and fails open when none does.

Version skew resolves newest-wins on first contact. When a CLI finds a daemon built from a strictly older binary, the freshly spawned daemon's `listen()` evicts the holder: it asks the old daemon to shut down over the socket, escalates to SIGKILL if it wedges, and waits for the old process to exit before binding. A same-or-newer holder is never evicted — the spawned daemon exits with an error instead, and the spawning client accepts the running daemon. The tie refusal is what prevents two daemons from evicting each other in a loop, and the newer-holder refusal is what stops sessions pinned to older plugin builds from tearing down the shared daemon every turn. A dev build counts as newest: a dev daemon is never evicted, and a dev binary always takes over a release daemon.

The HTTP plane binds a 127.0.0.1 port and publishes it to `~/.cc-review/v1/http.json` so the CLI and stream consumers can find it. The file is left in place on shutdown, and a booting daemon tries that previous port first before falling back to an ephemeral one, so printed review URLs survive a daemon swap. With `cc-review daemon --dev` the port is pinned to 8787, which is where the Vite dev proxy expects to find the API during frontend work.

Daemons spawned by the CLI append their stdout and stderr to `~/.cc-review/v1/daemon.log` — boot lines, eviction sequences, and panics all land there across daemon generations. A manual `cc-review daemon` run keeps its output on the terminal.

## Two planes

The daemon exposes two surfaces.

The **control plane** is a unix socket at `~/.cc-review/v1/daemon.sock` (mode 0600) speaking exact protocol v1. This is what CLI commands call. The ops dispatched in `internal/daemon` are `health`, `shutdown`, `start`, `resolve`, `reply`, `feedback`, `status`, `session-record`, `guard-edit`, `file-states`, `update-ai-request`, `submit-organization`, and `review-files`. A mismatched protocol is rejected before dispatch.

The **HTTP plane** (`internal/httpapi`) binds 127.0.0.1 only, and that bind is the entire access-control story. It serves the embedded SPA at `/`, a JSON REST surface, and one SSE stream. These routes are registered in `internal/httpapi/server.go`.

```
GET  /api/session/{reviewId}
GET  /api/session/{reviewId}/versions
POST /api/comments
PUT  /api/comments/{id}
POST /api/replies/{commentId}
POST /api/file-states
POST /api/ai-requests
POST /api/ai-requests/{id}/undo
POST /api/submit
GET  /events
```

## The event log

All realtime behavior rides on one append-only `events` table, keyed `(review_id, seq)`. The daemon's `AppendEvent` is the single chokepoint: it persists the row, then publishes a wakeup on an in-memory bus so parked SSE handlers re-read the log.

Delivery is at-least-once. `GET /events?session=<ref>` streams a review's log; consumers resume from their last sequence number via `Last-Event-ID`, or via the `?last_event_id=` query fallback since native `EventSource` cannot set headers on the initial request. The Claude-side consumers, `watch` and the MCP channel server, persist their cursor on disk per consumer, so a restart resumes without re-delivering.

Each event carries an `origin` of `user`, `claude`, or `system`. The browser subscribes with no filter and sees everything; Claude-side consumers pass `exclude_origin=claude` so they never receive an echo of their own replies. Duplicate suppression on the write side uses an optional `dedup_key` with a unique partial index, so a redelivered reply inserts once and re-emits nothing.

Named consumers also register presence: their attach and detach transitions drive `channel.changed` events, which is how the UI knows whether a live Claude session is wired to the review. `channel.changed` is delivered to the browser only — named consumer streams (`channel`, `watch`) filter it out, since a consumer learning about its own attachment is noise.

Presence alone never proves delivery: Claude Code silently drops channel notifications when channels are unavailable, so `channel: active` requires the model to have acknowledged a delivered tag via `channel-ack`. A `start` on an attached-but-unproven window solicits that proof by injecting a one-shot `channel.probe` frame into exactly that window's channel stream. The probe bypasses the event log and carries no SSE id — it cannot replay on reconnect, and neither the browser nor `watch` ever sees it. It lands while the model is mid-turn running the start skill, so no idle session is ever woken.

## Storage

State lives in a single SQLite database via `modernc.org/sqlite` (pure Go, no cgo). `internal/store` opens it with `SetMaxOpenConns(1)`, WAL journaling, and a 5s busy timeout; every other package goes through the store, never the driver.

The database carries an exact v1 schema marker and fingerprint. There are no migrations: a future schema epoch uses a fresh namespace, while the derived `~/.cc-review/v1` tree can be discarded and rebuilt. Large patches stay out of the database; a version row stores only the patch path and a files summary.

## State directory layout

`internal/paths` owns the layout under `~/.cc-review/v1`. The directory is always under the home directory; `CLAUDE_CONFIG_DIR` does not move it.

```
~/.cc-review/v1/
├── state.db                # SQLite database
├── daemon.sock             # control-plane unix socket
├── daemon.log              # spawned daemons append stdout/stderr here
├── http.json               # HTTP port handshake, kept across restarts for port reuse
├── channels-setup.json     # marker: the one-time channels offer was made
├── locks/
│   └── start.lock          # flock serializing lazy daemon starts
└── subjects/<review-id>/
    ├── snap_N.patch        # unified patch for version N
    ├── feedback_N.json     # frozen feedback for version N (written on Submit)
    ├── watch.cursor        # per-consumer last-delivered event seq
    └── channel.cursor
```

## Working-tree snapshots

`internal/vcs` turns a working copy's pending changes into a git-format patch. Detection walks upward from the cwd without spawning a subprocess. A `.jj` directory means jj; a `.git` entry means git, and the check accepts both a directory and a file since git worktrees use a file. In a colocated repo jj wins. Git diffs against `HEAD`, or against the empty tree in a fresh repo; jj diffs against the working-copy parent.

Each `start` captures a new snapshot and inserts a new version row, writing the patch to a temp file first and renaming it into place so a write failure can never leave a committed-but-unreadable version. Per-file fingerprints let reviewed marks survive across versions: a file stays marked reviewed exactly while its diff content is unchanged.

## The embedded SPA

The frontend is a Vite + React app in `web/`, built into `internal/web/dist` and compiled into the binary with `go:embed`. Because the embed happens at compile time, the web build must run before the Go build:

```sh
cd web && bunx vite build
cd .. && go build ./cmd/cc-review
```

A committed placeholder `index.html` keeps a clean tree compiling; a real build replaces it with hashed assets. The HTTP plane registers the SPA handler last and least-specific, so `/api` and `/events` always win routing.
