import { describe, expect, it } from 'vitest';
import { conversationByItem } from './conversation';
import { comment } from '../test/fixtures';

describe('conversationByItem', () => {
  it('counts open human threads and skips automation', () => {
    const byItem = conversationByItem([
      comment({ id: 'c1', author: 'remote', authorLogin: 'coworker' }),
      comment({ id: 'c2', author: 'automation', authorLogin: 'forge-pr-reviewer[bot]' }),
    ]);
    expect([...byItem.values()]).toEqual([{ openCount: 1, needsReply: false }]);
  });
});
