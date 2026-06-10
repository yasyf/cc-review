import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import type { Comment, Origin } from './types';

// Map of comment id → id of the newest thread entry the user has seen (a reply
// id, or the comment's own id when the thread has no replies). Persisted per
// review in localStorage; a thread is unread only while its newest entry came
// from Claude, so user-authored entries never need a write to stay read.
export type SeenMap = Record<string, string>;

const storageKey = (reviewId: string) => `cc-review:seen:${reviewId}`;

export function latestEntry(comment: Comment): { id: string; origin: Origin } {
  const last = comment.replies[comment.replies.length - 1];
  return last ? { id: last.id, origin: last.origin } : { id: comment.id, origin: comment.origin };
}

export function isUnread(comment: Comment, seen: SeenMap): boolean {
  const latest = latestEntry(comment);
  return latest.origin === 'claude' && seen[comment.id] !== latest.id;
}

export function unreadCount(comments: readonly Comment[], seen: SeenMap): number {
  return comments.filter((c) => isUnread(c, seen)).length;
}

function readSeen(reviewId: string): SeenMap {
  const raw = localStorage.getItem(storageKey(reviewId));
  return raw ? (JSON.parse(raw) as SeenMap) : {};
}

interface UnreadValue {
  seen: SeenMap;
  markSeen(comment: Comment): void;
}

const UnreadContext = createContext<UnreadValue | null>(null);

export function useUnread(): UnreadValue {
  const value = useContext(UnreadContext);
  if (!value) throw new Error('useUnread must be used within UnreadProvider');
  return value;
}

export function UnreadProvider({
  reviewId,
  comments,
  prune,
  children,
}: {
  reviewId: string;
  comments: readonly Comment[];
  // True only when viewing the latest version: pruning against a historical
  // version's comment set would erase the latest version's seen state.
  prune: boolean;
  children: ReactNode;
}) {
  const [seen, setSeen] = useState<SeenMap>(() => readSeen(reviewId));

  useEffect(() => {
    localStorage.setItem(storageKey(reviewId), JSON.stringify(seen));
  }, [reviewId, seen]);

  // Comments are per-version rows; drop entries for ids no longer present so
  // the map doesn't accumulate every superseded version's ids forever.
  useEffect(() => {
    if (!prune) return;
    const live = new Set(comments.map((c) => c.id));
    setSeen((prev) => {
      const kept = Object.entries(prev).filter(([id]) => live.has(id));
      return kept.length === Object.keys(prev).length ? prev : Object.fromEntries(kept);
    });
  }, [prune, comments]);

  const markSeen = useCallback((comment: Comment) => {
    const latest = latestEntry(comment);
    setSeen((prev) =>
      prev[comment.id] === latest.id ? prev : { ...prev, [comment.id]: latest.id },
    );
  }, []);

  return <UnreadContext.Provider value={{ seen, markSeen }}>{children}</UnreadContext.Provider>;
}
