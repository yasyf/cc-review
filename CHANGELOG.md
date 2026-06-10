# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/yasyf/cc-review/compare/v0.2.0...main
[0.2.0]: https://github.com/yasyf/cc-review/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/yasyf/cc-review/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/yasyf/cc-review/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/yasyf/cc-review/releases/tag/v0.1.0
