import { describe, expect, it } from 'vitest';
import { ciRollup, reviewRollup, scopeSession, stackCards } from './stack';
import { comment, pullRequest, section, session } from '../test/fixtures';
import type {
  Comment,
  PullRequest,
  PullRequestCheck,
  PullRequestReviewer,
  Section,
  SessionResponse,
} from './types';

function check(state: PullRequestCheck['state']): PullRequestCheck {
  return { name: `check-${state}`, state, url: '' };
}

function reviewer(state: PullRequestReviewer['state']): PullRequestReviewer {
  return { login: `r-${state}`, avatarUrl: '', state };
}

function stackSection(position: number, branch: string, prNumber: number, reviewed: string[], files: string[]): Section {
  return section({
    sectionId: String(position + 1),
    position,
    sectionKey: branch,
    branch,
    pending: false,
    prNumber,
    files: files.map((path) => ({ path, status: 'M' })),
    fileStates: Object.fromEntries(reviewed.map((path) => [path, { reviewed: true, hidden: false }])),
  });
}

function stackComment(id: string, branch: string, overrides: Partial<Comment> = {}): Comment {
  return comment({ id, branch, pending: false, ...overrides });
}

function stackSession(sections: Section[], comments: Comment[], pullRequests: PullRequest[]): SessionResponse {
  return session({ sections, comments, pullRequests });
}

describe('ciRollup', () => {
  it.each([
    { name: 'no checks', checks: [], want: 'none' },
    { name: 'all passing or neutral', checks: [check('SUCCESS'), check('NEUTRAL'), check('SKIPPED')], want: 'success' },
    { name: 'one pending', checks: [check('SUCCESS'), check('PENDING')], want: 'pending' },
    { name: 'failure beats pending', checks: [check('PENDING'), check('FAILURE')], want: 'failure' },
  ])('$name', ({ checks, want }) => {
    expect(ciRollup(checks)).toBe(want);
  });
});

describe('reviewRollup', () => {
  it.each([
    { name: 'no reviewers', reviewers: [], want: 'none' },
    { name: 'requested only', reviewers: [reviewer('PENDING')], want: 'pending' },
    { name: 'commented beats requested', reviewers: [reviewer('PENDING'), reviewer('COMMENTED')], want: 'commented' },
    { name: 'approved beats commented', reviewers: [reviewer('COMMENTED'), reviewer('APPROVED')], want: 'approved' },
    {
      name: 'changes requested beats approved',
      reviewers: [reviewer('APPROVED'), reviewer('CHANGES_REQUESTED')],
      want: 'changes_requested',
    },
  ])('$name', ({ reviewers, want }) => {
    expect(reviewRollup(reviewers)).toBe(want);
  });
});

describe('stackCards', () => {
  const sections = [stackSection(0, 'b1', 1, ['a.go'], ['a.go', 'b.go']), stackSection(1, 'b2', 2, [], ['c.go'])];
  const pulls = [
    pullRequest({ number: 1, title: 'PR 1', checks: [check('SUCCESS')], reviewers: [reviewer('APPROVED')] }),
    pullRequest({ number: 2, title: 'Top', checks: [check('FAILURE')] }),
  ];
  const comments = [
    stackComment('c1', 'b1', { author: 'remote', authorLogin: 'coworker' }),
    stackComment('c2', 'b1', { status: 'resolved' }),
    stackComment('c3', 'b2', { author: 'claude', origin: 'claude' }),
    stackComment('c4', 'b1', { author: 'automation', authorLogin: 'graphite-app[bot]' }),
  ];

  it('derives one card per section in stack order', () => {
    expect(stackCards(stackSession(sections, comments, pulls), { c3: 'c3' })).toEqual([
      {
        sectionKey: 'b1',
        title: 'PR 1',
        branch: 'b1',
        pr: pulls[0],
        ci: 'success',
        review: 'approved',
        unread: 1,
        open: 1,
        reviewed: 1,
        total: 2,
      },
      {
        sectionKey: 'b2',
        title: 'Top',
        branch: 'b2',
        pr: pulls[1],
        ci: 'failure',
        review: 'none',
        unread: 0,
        open: 1,
        reviewed: 0,
        total: 1,
      },
    ]);
  });

  it('falls back to the branch for a local stack section', () => {
    const [card] = stackCards(stackSession([stackSection(0, 'feature', 0, [], ['a.go'])], [], []), {});
    expect(card).toEqual({
      sectionKey: 'feature',
      title: 'feature',
      branch: 'feature',
      pr: null,
      ci: 'none',
      review: 'none',
      unread: 0,
      open: 0,
      reviewed: 0,
      total: 1,
    });
  });
});

describe('scopeSession', () => {
  const full = stackSession(
    [stackSection(0, 'b1', 1, [], ['a.go']), stackSection(1, 'b2', 2, [], ['c.go'])],
    [stackComment('c1', 'b1'), stackComment('c2', 'b2')],
    [pullRequest({ number: 1, title: 'PR 1' }), pullRequest({ number: 2, title: 'PR 2' })],
  );

  it('returns the session untouched with no scope', () => {
    expect(scopeSession(full, undefined)).toBe(full);
  });

  it('keeps only the scoped section and its comments', () => {
    const scoped = scopeSession(full, 'b2');
    expect(scoped.sections.map((s) => s.sectionKey)).toEqual(['b2']);
    expect(scoped.comments.map((c) => c.id)).toEqual(['c2']);
    expect(scoped.pullRequests).toBe(full.pullRequests);
  });
});
