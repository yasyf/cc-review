import type { FileRef } from './diff';

// Heuristic glob matcher over the diff's own file refs (every section's files)
// ONLY. Supports *, **, ? and a bare substring (a token with no glob char and no
// slash matches anywhere in the path, so "lock" finds web/bun.lock). It can
// diverge from the agent's matching, so callers must treat the result as a live
// hint for preflight — never a gate.

// Sentinel standing in for ** between the single-* expansion; a NUL can't occur
// in a path or a glob, so it never collides with literal pattern characters.
const STARSTAR = '\x00';

function globToRegExp(pattern: string): RegExp {
  const body = pattern
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*\*/g, STARSTAR)
    .replace(/\*/g, '[^/]*')
    .replaceAll(STARSTAR, '.*')
    .replace(/\?/g, '[^/]');
  return new RegExp(`^${body}$`);
}

export function matchFiles(files: readonly FileRef[], pattern: string): FileRef[] {
  const trimmed = pattern.trim();
  if (trimmed === '') return [];
  if (!/[*?]/.test(trimmed) && !trimmed.includes('/')) {
    const needle = trimmed.toLowerCase();
    return files.filter((f) => f.path.toLowerCase().includes(needle));
  }
  const re = globToRegExp(trimmed);
  // Also test the basename so a bare "*.lock" matches at any depth.
  return files.filter((f) => re.test(f.path) || re.test(f.path.split('/').pop() ?? ''));
}
