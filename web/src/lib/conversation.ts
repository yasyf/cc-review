// Per-item rollup of open comment threads for the sidebar panels, keyed by
// itemId so a path in two sections is counted per section.

import { commentItemId } from './diff';
import type { Comment } from './types';

export interface FileConversation {
  openCount: number;
  needsReply: boolean;
}

// Replies arrive oldest-first, so "answered" for a question is positional:
// any later user reply closes it. Asks carry their own answered flag.
function awaitsUser(comment: Comment): boolean {
  return comment.replies.some((reply, index) => {
    if (reply.origin !== 'claude') return false;
    if (reply.kind === 'ask') return reply.answered !== true;
    if (reply.kind !== 'question') return false;
    return !comment.replies.slice(index + 1).some((later) => later.origin === 'user');
  });
}

export function conversationByItem(comments: Comment[]): Map<string, FileConversation> {
  const byItem = new Map<string, FileConversation>();
  for (const comment of comments) {
    // Claude-authored comments are informational annotations, not reviewer TODOs.
    if (comment.status !== 'open' || comment.origin === 'claude') continue;
    const id = commentItemId(comment);
    const entry = byItem.get(id) ?? { openCount: 0, needsReply: false };
    entry.openCount += 1;
    entry.needsReply = entry.needsReply || awaitsUser(comment);
    byItem.set(id, entry);
  }
  return byItem;
}
