import { describe, expect, it } from 'vitest';
import { isAutomated } from './automation';
import { comment } from '../test/fixtures';

describe('isAutomated', () => {
  it.each([
    { author: 'automation' as const, want: true },
    { author: 'remote' as const, want: false },
    { author: 'user' as const, want: false },
    { author: 'claude' as const, want: false },
  ])('$author → $want', ({ author, want }) => {
    expect(isAutomated(comment({ author }))).toBe(want);
  });
});
