import { createEventStream } from '@cc-interact/react';
import type { StreamToast } from '@cc-interact/react';
import { sessionKey } from './api';
import type { Comment, Reply, ReviewEvent, SessionResponse } from './types';

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
      // feedbackPath is client-only state folded into the cache here; SubmitBar
      // reads it back from the session rather than from the stream value.
      return {
        ...session,
        review: { ...session.review, status: 'submitted' },
        feedbackPath: ev.feedbackPath,
      };
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
    case 'annotations.updated':
      return { ...session, annotations: ev.annotations };
    case 'channel.changed':
    case 'version.created':
    case 'notification':
      return session;
  }
}

function notificationFor(ev: ReviewEvent): StreamToast | null {
  switch (ev.type) {
    case 'notification':
      return { kind: ev.level, text: ev.message };
    case 'claude.question':
    case 'claude.ask':
      return { kind: 'info', text: `Claude asked: ${ev.reply.body}` };
    case 'claude.clarification':
      return { kind: 'info', text: `Claude clarified: ${ev.reply.body}` };
    case 'submit':
      return { kind: 'info', text: 'Review submitted — feedback frozen.' };
    case 'ai.request.updated':
      if (ev.request.status === 'done') {
        return { kind: 'info', text: ev.request.summary || 'AI request done.' };
      }
      if (ev.request.status === 'failed') {
        return { kind: 'error', text: ev.request.summary || 'AI request failed.' };
      }
      if (ev.request.status === 'awaiting_input') {
        return { kind: 'info', text: `Claude needs your input: ${ev.request.question?.body ?? ''}` };
      }
      return null;
    case 'organization.updated':
      return {
        kind: 'info',
        text: `Review organized into ${ev.organization.chapters.length} chapters.`,
      };
    default:
      return null;
  }
}

// ai.request.* are stamped with the review's CURRENT version at emit time, but
// replayed historical frames carry the then-current version — so they must apply
// regardless of the version on screen. Their reducers are id-keyed upserts, which
// makes that safe. Everything else only patches the version it belongs to.
// (channel.changed drives presence via peerPresence, not the cache, so it does
// not need to apply here.)
function versionAgnostic(ev: ReviewEvent): boolean {
  return ev.type === 'ai.request.created' || ev.type === 'ai.request.updated';
}

const { EventStreamProvider, useEventStream } = createEventStream<
  ReviewEvent,
  SessionResponse,
  string,
  number | undefined
>({
  queryKey: (slug, version) => sessionKey(slug, version ?? 'latest'),
  reduce: reduceSession,
  toast: notificationFor,
  appliesTo: (ev, cache) => versionAgnostic(ev) || ev.version_number === cache.version,
  highWaterSeq: (cache) => Number(cache.latestEventSeq),
  peerPresence: (ev) => (ev.type === 'channel.changed' ? ev.connected : null),
  initialPeerPresence: (cache) => cache.claudeConnected,
  // A new version supersedes the cached session wholesale; the > guard keeps the
  // cursor-0 replay of historical versions from refetch-spamming.
  onEvent: (ev, { queryClient, cache, subject }) => {
    if (ev.type === 'version.created' && cache && ev.version_number > cache.version) {
      void queryClient.invalidateQueries({ queryKey: ['session', subject], exact: false });
    }
  },
});

export { EventStreamProvider, useEventStream };
