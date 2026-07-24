# cc-review Development Guide

Local code-review daemon + Claude plugin — review what Claude writes in a PR-like web UI and stream feedback back.

## Repository Structure

```
cc-review/
├── cmd/cc-review/      # Binary entrypoint — cobra root wiring CLI + hidden `daemon`
├── internal/           # Go core (not importable outside the module)
│   ├── cli/            #   cobra subcommands: start, watch, reply, feedback, status, stop, …
│   ├── daemon/         #   lazy-started daemon: unix-socket IPC, pub/sub bus, long-poll
│   ├── httpapi/        #   127.0.0.1 HTTP server: embedded SPA, /api REST, /events SSE
│   ├── store/          #   modernc.org/sqlite, exact-schema DDL (archive-on-change) + queries
│   ├── paths/          #   ~/.cc-review state-dir layout
│   ├── version/        #   ldflags-injected version
│   └── web/            #   go:embed of the built SPA (dist/)
├── web/                # Frontend source: Vite + React + @pierre/diffs (TanStack Router/Query)
├── plugin/             # The self-contained plugin payload — the only directory that installs
│   ├── .claude-plugin/ #   plugin.json: manifest with mcpServers (channel server) + channels
│   ├── skills/start/   #   the /cc-review:start skill (thin CLI wrapper) + reference docs
│   ├── hooks/          #   SessionStart, PreToolUse edit-guard
│   ├── scripts/        #   install-binary.sh (rendered by cc-guides), mcp-channel.sh
│   └── bin/            #   cc-review symlink — brew binary, data-dir payload, or dev build (gitignored)
├── .claude-plugin/     # marketplace.json — plugin source points at ./plugin
├── AGENTS.md           # This file — shared conventions
└── README.md           # Project overview
```

The VCS snapshot layer (git, jj, and Graphite-stack capture) and session/window resolution live in the `github.com/yasyf/cc-interact` module, not under `internal/`.
