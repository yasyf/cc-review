import { describe, expect, it } from 'vitest';
import { relativeTime } from './time';

describe('relativeTime', () => {
  const now = Date.parse('2026-10-07T12:00:00Z');

  it.each([
    ['2026-10-07T11:59:40Z', 'just now'],
    ['2026-10-07T11:55:00Z', '5m ago'],
    ['2026-10-07T09:00:00Z', '3h ago'],
    ['2026-10-06T12:00:00Z', 'yesterday'],
    ['2026-09-20T12:00:00Z', '2w ago'],
  ])('%s → %s', (iso, want) => {
    expect(relativeTime(iso, now)).toBe(want);
  });
});
