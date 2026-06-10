import { createContext, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { sessionKey } from './api';
import type { Comment, Notification, Reply, ReviewEvent, SessionResponse } from './types';

interface EventStreamValue {
  connected: boolean;
  notifications: Notification[];
  dismiss(id: string): void;
  feedbackPath: string | null;
}

const EventStreamContext = createContext<EventStreamValue | null>(null);

export function useEventStream(): EventStreamValue {
  const value = useContext(EventStreamContext);
  if (!value) throw new Error('useEventStream must be used within EventStreamProvider');
  return value;
}

function upsertReply(comment: Comment, reply: Reply): Comment {
  const exists = comment.replies.some((r) => r.id === reply.id);
  const replies = exists
    ? comment.replies.map((r) => (r.id === reply.id ? reply : r))
    : [...comment.replies, reply];
  return { ...comment, replies };
}

function putComment(session: SessionResponse, comment: Comment): SessionResponse {
  const exists = session.comments.some((c) => c.id === comment.id);
  const comments = exists
    ? session.comments.map((c) => (c.id === comment.id ? comment : c))
    : [...session.comments, comment];
  return { ...session, comments };
}

function reduceSession(session: SessionResponse, ev: ReviewEvent): SessionResponse {
  switch (ev.type) {
    case 'comment.created':
    case 'comment.updated':
      return putComment(session, ev.comment);
    case 'comment.resolved':
      return {
        ...session,
        comments: session.comments.map((c) =>
          c.id === ev.commentId ? { ...c, status: 'resolved' } : c,
        ),
      };
    case 'claude.question':
    case 'claude.ask':
    case 'claude.clarification':
      return {
        ...session,
        comments: session.comments.map((c) =>
          c.id === ev.commentId ? upsertReply(c, ev.reply) : c,
        ),
      };
    case 'status.changed':
      return { ...session, review: { ...session.review, status: ev.status } };
    case 'submit':
      return { ...session, review: { ...session.review, status: 'submitted' } };
    case 'notification':
      return session;
  }
}

function notificationFor(ev: ReviewEvent): Omit<Notification, 'id' | 'at'> | null {
  switch (ev.type) {
    case 'notification':
      return { level: ev.level, message: ev.message };
    case 'claude.question':
    case 'claude.ask':
      return { level: 'info', message: `Claude asked: ${ev.reply.body}` };
    case 'claude.clarification':
      return { level: 'info', message: `Claude clarified: ${ev.reply.body}` };
    case 'submit':
      return { level: 'info', message: 'Review submitted — feedback frozen.' };
    default:
      return null;
  }
}

let notificationSeq = 0;

export function EventStreamProvider({
  slug,
  version,
  children,
}: {
  slug: string;
  version?: number;
  children: ReactNode;
}) {
  const qc = useQueryClient();
  const [connected, setConnected] = useState(false);
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [feedbackPath, setFeedbackPath] = useState<string | null>(null);

  useEffect(() => {
    const url = `/events?session=${encodeURIComponent(slug)}`;
    const source = new EventSource(url);

    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);

    source.onmessage = (raw: MessageEvent<string>) => {
      const ev = JSON.parse(raw.data) as ReviewEvent;

      const key = sessionKey(slug, version ?? 'latest');
      const current = qc.getQueryData<SessionResponse>(key);
      // Only patch when the frame belongs to the version on screen.
      if (current && ev.version_number === current.version) {
        qc.setQueryData<SessionResponse>(key, reduceSession(current, ev));
      }

      if (ev.type === 'submit') setFeedbackPath(ev.feedbackPath);

      const note = notificationFor(ev);
      if (note) {
        const entry: Notification = { ...note, id: `n${++notificationSeq}`, at: Date.now() };
        setNotifications((prev) => [...prev, entry].slice(-50));
      }
    };

    return () => source.close();
  }, [qc, slug, version]);

  function dismiss(id: string) {
    setNotifications((prev) => prev.filter((n) => n.id !== id));
  }

  return (
    <EventStreamContext.Provider value={{ connected, notifications, dismiss, feedbackPath }}>
      {children}
    </EventStreamContext.Provider>
  );
}
