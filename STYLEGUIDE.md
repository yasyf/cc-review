# cc-review Style Guide

The concrete style rules for this repository.

## Core Principles

1. **Fail fast, fail loud.** No defensive coding: no fallbacks, shims, or
   backwards-compat layers, and no guards against impossible states. No sentinel
   values, no silent defaults. If unused, delete it. Crash on the unexpected.
2. **Make invalid states unrepresentable.** Branded/newtype primitives, immutable
   data structures, required fields over optionals.
3. **Minimal changes.** Stay within scope. Make the test pass, then stop. Improve
   only the code you touch.
4. **Match surrounding code.** Follow this guide first, then the file you're in,
   then the module. If surrounding code violates this guide, fix it.

### Go (the daemon + CLI)

- Keep packages small and single-purpose under `internal/`; the public surface is the
  `cmd/cc-review` binary, not importable Go API. Lowercase unexported by default; export
  only what another package must call.
- Wrap errors with context using `fmt.Errorf("doing X: %w", err)` and let them propagate;
  no naked `panic` in library code, no swallowed errors. The daemon's top loop logs and
  continues; the CLI exits non-zero with the wrapped message.
- Pass `context.Context` as the first argument through the daemon, IPC, and DB calls;
  cancellation is how a killed Bash long-poll frees a parked goroutine.
- SQLite access goes through `internal/store` only, on a single writer connection
  (`SetMaxOpenConns(1)`, WAL); never hold a connection while a long-poll is parked.
- Table-driven tests with `t.Run` subtests; use a real ephemeral on-disk SQLite for store
  tests, not a mock.

```go
// Good — wrapped, contextual, propagated
if err := store.AppendEvent(ctx, ev); err != nil {
    return fmt.Errorf("append %s event: %w", ev.Type, err)
}
// Bad — context lost, error swallowed
store.AppendEvent(ctx, ev)
```

### TypeScript / React (the `web/` frontend)

- Function components with hooks only. Server/daemon state lives in TanStack Query
  (`useQuery`/`useMutation`); component state is for ephemeral UI only — never mirror
  fetched data into `useState`.
- `strict` TypeScript; no `any`. Model daemon payloads as explicit types/discriminated
  unions (the event stream is a tagged union on `type`) so the compiler enforces handling
  each event kind.
- The realtime layer is one `EventSource` that patches the Query cache; components read
  from the cache and never open their own sockets.
- Keep `@pierre/diffs` interaction in dedicated components (`DiffView`, `CommentThread`);
  the rest of the app talks to it through props, not the library directly.

## Error Handling

Keep error-handling blocks minimal: only the operation that can fail belongs
inside. No catch-all handlers that swallow everything; use dedicated error types.
Read required configuration so a missing key fails at startup. No sentinel return
values; raise, or return a typed result.

## Code Organization

Order each module: imports, constants, type aliases, helpers, classes, then
functions. Constants sit immediately after imports, before any class or function.
Use the language's export-control mechanism instead of underscore/naming
conventions to hide internals.

## Comments & Docstrings

Code documents itself through names, types, and organization. No comments except
TODOs, non-obvious workarounds, or disabled code. Document the public API only;
a doc comment that restates the signature is clutter to delete.

## Testing

Write strict assertions against specific expected values; a test that can't fail
uncovers nothing. Mock the boundaries your code talks to, such as the network,
filesystem, and clock, and leave the function under test real. A database (or any
stateful service) is not a mock boundary: when a test needs one, start a real
ephemeral instance with testcontainers rather than mocking the driver or using an
in-memory fake. Parameterize repeated test bodies, giving each case a descriptive
id and its own expected values.
