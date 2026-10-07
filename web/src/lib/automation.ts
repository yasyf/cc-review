import type { Comment } from './types';

export function isAutomated(comment: Pick<Comment, 'author'>): boolean {
  return comment.author === 'automation';
}
