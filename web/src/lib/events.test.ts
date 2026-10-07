import { describe, expect, it } from 'vitest';
import { aiRequest, comment, reply, section, session } from '../test/fixtures';
import { reduceSession } from './events';
import type { Organization, ReviewEvent } from './types';

describe('reduceSession', () => {
  it('appends a created comment', () => {
    const next = reduceSession(session(), {
      type: 'comment.created',
      version_number: 1,
      commentId: 'c1',
      comment: comment(),
    });
    expect(next.comments.map((c) => c.id)).toEqual(['c1']);
  });

  it('replaces an updated comment in place', () => {
    const before = session({ comments: [comment(), comment({ id: 'c2' })] });
    const next = reduceSession(before, {
      type: 'comment.updated',
      version_number: 1,
      commentId: 'c1',
      comment: comment({ body: 'edited' }),
    });
    expect(next.comments.map((c) => [c.id, c.body])).toEqual([
      ['c1', 'edited'],
      ['c2', 'why?'],
    ]);
  });

  it('resolves only the named comment', () => {
    const before = session({ comments: [comment(), comment({ id: 'c2' })] });
    const next = reduceSession(before, { type: 'comment.resolved', version_number: 1, commentId: 'c2' });
    expect(next.comments.map((c) => c.status)).toEqual(['open', 'resolved']);
  });

  it.each(['claude.question', 'claude.ask', 'claude.clarification'] as const)(
    '%s upserts the reply under its comment',
    (type) => {
      const before = session({ comments: [comment({ replies: [reply()] })] });
      const once = reduceSession(before, { type, version_number: 1, commentId: 'c1', reply: reply({ body: 'new' }) });
      const added = reduceSession(once, { type, version_number: 1, commentId: 'c1', reply: reply({ id: 'r2' }) });
      expect(added.comments[0].replies.map((r) => [r.id, r.body])).toEqual([
        ['r1', 'new'],
        ['r2', 'because'],
      ]);
    },
  );

  it('applies status changes', () => {
    const next = reduceSession(session(), { type: 'status.changed', version_number: 1, status: 'expired' });
    expect(next.review.status).toBe('expired');
  });

  it('freezes the review on submit and keeps the feedback path', () => {
    const next = reduceSession(session(), { type: 'submit', version_number: 1, feedbackPath: '/tmp/fb.md' });
    expect(next.review.status).toBe('submitted');
    expect(next.feedbackPath).toBe('/tmp/fb.md');
  });

  it('merges file states per section idempotently', () => {
    const before = session({
      sections: [
        section({ sectionKey: '', fileStates: { 'a.ts': { reviewed: false, hidden: false } } }),
        section({ sectionKey: 'feat/y', fileStates: { 'b.ts': { reviewed: true, hidden: false } } }),
      ],
    });
    const ev: ReviewEvent = {
      type: 'file.states',
      version_number: 1,
      states: [
        { sectionKey: '', path: 'a.ts', reviewed: true, hidden: false },
        { sectionKey: '', path: 'c.ts', reviewed: false, hidden: true },
      ],
    };
    const once = reduceSession(before, ev);
    const twice = reduceSession(once, ev);
    expect(twice).toEqual(once);
    expect(once.sections[0].fileStates).toEqual({
      'a.ts': { reviewed: true, hidden: false },
      'c.ts': { reviewed: false, hidden: true },
    });
    expect(once.sections[1]).toBe(before.sections[1]);
  });

  it('prepends new AI requests and replaces updated ones', () => {
    const before = session({ aiRequests: [aiRequest()] });
    const created = reduceSession(before, {
      type: 'ai.request.created',
      version_number: 1,
      request: aiRequest({ id: 'ai2' }),
    });
    const updated = reduceSession(created, {
      type: 'ai.request.updated',
      version_number: 1,
      request: aiRequest({ status: 'done' }),
    });
    expect(updated.aiRequests.map((r) => [r.id, r.status])).toEqual([
      ['ai2', 'pending'],
      ['ai1', 'done'],
    ]);
  });

  it('sets the organization on the matching section only', () => {
    const organization: Organization = { overview: 'o', chapters: [] };
    const before = session({ sections: [section({ sectionKey: '' }), section({ sectionKey: 'feat/y' })] });
    const next = reduceSession(before, {
      type: 'organization.updated',
      version_number: 1,
      sectionKey: 'feat/y',
      organization,
    });
    expect(next.sections.map((s) => s.organization)).toEqual([null, organization]);
  });

  it('replaces annotations wholesale', () => {
    const annotation = {
      id: 'a1',
      sectionKey: '',
      filePath: 'a.ts',
      side: 'additions' as const,
      start: 1,
      end: 2,
      label: 'look',
      createdAt: '2026-10-01T00:00:00Z',
    };
    const next = reduceSession(session(), { type: 'annotations.updated', version_number: 1, annotations: [annotation] });
    expect(next.annotations).toEqual([annotation]);
  });

  it.each<ReviewEvent>([
    { type: 'channel.changed', version_number: 1, connected: true },
    { type: 'version.created', version_number: 2 },
    { type: 'notification', version_number: 1, level: 'info', message: 'hi' },
  ])('leaves the session untouched for $type', (ev) => {
    const before = session();
    expect(reduceSession(before, ev)).toBe(before);
  });
});
