import { describe, expect, it } from 'vitest';
import { isUnread } from './unread';
import { comment } from '../test/fixtures';

describe('isUnread', () => {
  it.each([
    { author: 'remote' as const, want: true },
    { author: 'claude' as const, want: true },
    { author: 'user' as const, want: false },
    { author: 'automation' as const, want: false },
  ])('a thread by $author is unread: $want', ({ author, want }) => {
    expect(isUnread(comment({ id: 'c1', author }), {})).toBe(want);
  });
});
