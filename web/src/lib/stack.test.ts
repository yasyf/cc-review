import { describe, expect, it } from 'vitest';
import { ciRollup, isOpenHumanThread, reviewRollup, scopeSession, stackRows, trunkBranch } from './stack';
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

function stackSection(
  position: number,
  branch: string,
  prNumber: number,
  reviewed: string[],
  files: string[],
  patchText = '',
): Section {
  return section({
    patchText,
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

describe('stackRows', () => {
  const sections = [
    stackSection(0, 'b1', 1, ['a.go'], ['a.go', 'b.go']),
    stackSection(1, 'b2', 2, [], ['c.go'], 'diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n@@ -1,2 +1,3 @@\n-old\n+new\n+more\n ctx'),
  ];
  const pulls = [
    pullRequest({ number: 1, title: 'PR 1', checks: [check('SUCCESS')], reviewers: [reviewer('APPROVED')] }),
    pullRequest({ number: 2, title: 'Top', checks: [check('FAILURE')] }),
  ];
  const comments = [
    stackComment('c1', 'b1', { author: 'remote', authorLogin: 'coworker' }),
    stackComment('c2', 'b1', { status: 'resolved' }),
    stackComment('c3', 'b2', { author: 'claude', origin: 'claude' }),
    stackComment('c4', 'b2', { author: 'automation', authorLogin: 'graphite-app[bot]', body: 'Merge activity' }),
    stackComment('c5', 'b2', { author: 'automation', authorLogin: 'yasyf', body: '[(View in Graphite)](https://app.graphite.dev/x)' }),
  ];

  it('lists the top of the stack first and trunk-most last', () => {
    const rows = stackRows(stackSession(sections, comments, pulls), { c3: 'c3' });
    expect(rows).toEqual([
      {
        sectionKey: 'b2',
        title: 'Top',
        branch: 'b2',
        pr: pulls[1],
        ci: 'failure',
        review: 'none',
        threads: 0,
        unread: 0,
        reviewed: 0,
        total: 1,
        additions: 2,
        deletions: 1,
      },
      {
        sectionKey: 'b1',
        title: 'PR 1',
        branch: 'b1',
        pr: pulls[0],
        ci: 'success',
        review: 'approved',
        threads: 1,
        unread: 1,
        reviewed: 1,
        total: 2,
        additions: 0,
        deletions: 0,
      },
    ]);
  });

  it('falls back to the branch for a local stack section', () => {
    const [row] = stackRows(stackSession([stackSection(0, 'feature', 0, [], ['a.go'])], [], []), {});
    expect(row.title).toBe('feature');
    expect(row.pr).toBeNull();
    expect(row.ci).toBe('none');
  });
});

describe('isOpenHumanThread', () => {
  it.each([
    { name: 'an open coworker thread', overrides: { author: 'remote' as const, authorLogin: 'sikanhe' }, want: true },
    { name: 'the viewer', overrides: {}, want: true },
    { name: 'resolved', overrides: { status: 'resolved' as const }, want: false },
    { name: 'claude', overrides: { author: 'claude' as const, origin: 'claude' as const }, want: false },
    { name: 'automation', overrides: { author: 'automation' as const, authorLogin: 'forge-pr-reviewer[bot]' }, want: false },
  ])('$name', ({ overrides, want }) => {
    expect(isOpenHumanThread(comment(overrides))).toBe(want);
  });
});

describe('trunkBranch', () => {
  it("names the bottom PR's base", () => {
    const full = stackSession(
      [stackSection(0, 'b1', 1, [], [])],
      [],
      [pullRequest({ number: 1, baseRefName: 'dev' })],
    );
    expect(trunkBranch(full)).toBe('dev');
  });

  it("falls back to the bottom section's parent", () => {
    expect(trunkBranch(stackSession([stackSection(0, 'b1', 0, [], [])], [], []))).toBe('main');
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
