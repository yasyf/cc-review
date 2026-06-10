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
    case 'file.states': {
      // Entries are absolute per-path values, so merging is idempotent under
      // the cursor-0 replay every page load performs.
      const fileStates = { ...session.fileStates };
      for (const state of ev.states) {
        fileStates[state.path] = { reviewed: state.reviewed, hidden: state.hidden };
      }
      return { ...session, fileStates };
    }
    case 'ai.request.created':
    case 'ai.request.updated': {
      const exists = session.aiRequests.some((r) => r.id === ev.request.id);
      const aiRequests = exists
        ? session.aiRequests.map((r) => (r.id === ev.request.id ? ev.request : r))
        : [ev.request, ...session.aiRequests];
      return { ...session, aiRequests };
    }
    case 'organization.updated':
      return { ...session, organization: ev.organization };
    case 'channel.changed':
      return { ...session, claudeConnected: ev.connected };
    case 'version.created':
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
    case 'ai.request.updated':
      if (ev.request.status === 'done') {
        return { level: 'info', message: ev.request.summary || 'AI request done.' };
      }
      if (ev.request.status === 'failed') {
        return { level: 'error', message: ev.request.summary || 'AI request failed.' };
      }
      return null;
    case 'organization.updated':
      return {
        level: 'info',
        message: `Review organized into ${ev.organization.chapters.length} chapters.`,
      };
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
      // channel.changed and ai.request.* are stamped with the review's CURRENT
      // version at emit time, but replayed historical frames carry the
      // then-current version — so they must apply regardless of the version on
      // screen. Their reducers are last-wins / id-keyed upserts, which makes
      // that safe. Everything else only patches the version it belongs to.
      const versionAgnostic =
        ev.type === 'channel.changed' ||
        ev.type === 'ai.request.created' ||
        ev.type === 'ai.request.updated';
      if (current && (versionAgnostic || ev.version_number === current.version)) {
        qc.setQueryData<SessionResponse>(key, reduceSession(current, ev));
      }

      // A new version supersedes the cached session wholesale; the > guard
      // keeps the cursor-0 replay of historical versions from refetch-spamming.
      if (ev.type === 'version.created' && current && ev.version_number > current.version) {
        void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
      }

      if (ev.type === 'submit') setFeedbackPath(ev.feedbackPath);

      // The stream replays the whole event log from cursor 0 on every load;
      // replayed frames (seq at or below the session snapshot's max) must keep
      // patching state above but never toast.
      const live = current !== undefined && Number(raw.lastEventId) > Number(current.latestEventSeq);
      const note = live ? notificationFor(ev) : null;
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
