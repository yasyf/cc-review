import { isAutomated } from './automation';
import type { Comment } from './types';

export function isConversation(comment: Comment): boolean {
  return comment.filePath === '';
}

export function isInlineThread(comment: Comment): boolean {
  return comment.subject === 'line' && !comment.outdated && !isAutomated(comment);
}

export function isStripThread(comment: Comment): boolean {
  return !isConversation(comment) && (comment.subject === 'file' || comment.outdated) && !isAutomated(comment);
}

export function fileThreads(comments: readonly Comment[]): Comment[] {
  return comments.filter((c) => !isConversation(c) && !isAutomated(c));
}

export function conversationThreads(comments: readonly Comment[]): Comment[] {
  return comments.filter((c) => isConversation(c) && !isAutomated(c));
}

export function automatedThreads(comments: readonly Comment[]): Comment[] {
  return comments.filter(isAutomated);
}
