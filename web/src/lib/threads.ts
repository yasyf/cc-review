import { commentSectionKey } from './diff/items';
import type { Comment } from './types';

export function isConversation(comment: Comment): boolean {
  return comment.filePath === '';
}

export function isLineThread(comment: Comment): boolean {
  return comment.subject === 'line';
}

export function fileThreads(comments: readonly Comment[]): Comment[] {
  return comments.filter((c) => !isConversation(c));
}

export function conversationThreads(comments: readonly Comment[]): Comment[] {
  return comments.filter(isConversation);
}

export function fileHeaderThreads(comments: readonly Comment[], sectionKey: string, path: string): Comment[] {
  return comments.filter(
    (c) => !isLineThread(c) && c.filePath === path && commentSectionKey(c) === sectionKey,
  );
}
