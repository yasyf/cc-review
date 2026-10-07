import { commentSectionKey } from './diff/items';
import { isUnread } from './unread';
import type { SeenMap } from './unread';
import type { PullRequest, PullRequestCheck, PullRequestReviewer, Section, SessionResponse } from './types';

export type CiState = 'success' | 'failure' | 'pending' | 'none';
export type ReviewState = 'approved' | 'changes_requested' | 'commented' | 'pending' | 'none';

export interface StackCard {
  sectionKey: string;
  title: string;
  branch: string;
  pr: PullRequest | null;
  ci: CiState;
  review: ReviewState;
  unread: number;
  open: number;
  reviewed: number;
  total: number;
}

export function ciRollup(checks: readonly PullRequestCheck[]): CiState {
  if (checks.length === 0) return 'none';
  if (checks.some((c) => c.state === 'FAILURE')) return 'failure';
  if (checks.some((c) => c.state === 'PENDING')) return 'pending';
  return 'success';
}

export function reviewRollup(reviewers: readonly PullRequestReviewer[]): ReviewState {
  const states = new Set(reviewers.map((r) => r.state));
  if (states.has('CHANGES_REQUESTED')) return 'changes_requested';
  if (states.has('APPROVED')) return 'approved';
  if (states.has('COMMENTED')) return 'commented';
  if (states.has('PENDING')) return 'pending';
  return 'none';
}

export function sectionPullRequest(
  section: Section,
  pullRequests: readonly PullRequest[],
): PullRequest | null {
  return pullRequests.find((pr) => pr.number === section.prNumber) ?? null;
}

export function stackCards(session: SessionResponse, seen: SeenMap): StackCard[] {
  return session.sections.map((section) => {
    const pr = sectionPullRequest(section, session.pullRequests);
    const comments = session.comments.filter(
      (c) => commentSectionKey(c) === section.sectionKey,
    );
    return {
      sectionKey: section.sectionKey,
      title: pr?.title ?? (section.pending ? 'Working tree' : section.branch),
      branch: section.branch,
      pr,
      ci: pr ? ciRollup(pr.checks) : 'none',
      review: pr ? reviewRollup(pr.reviewers) : 'none',
      unread: comments.filter((c) => isUnread(c, seen)).length,
      open: comments.filter((c) => c.status === 'open' && c.author !== 'automation').length,
      reviewed: section.files.filter((f) => section.fileStates[f.path]?.reviewed).length,
      total: section.files.length,
    };
  });
}

export function scopeSession(session: SessionResponse, sectionKey: string | undefined): SessionResponse {
  if (sectionKey === undefined) return session;
  return {
    ...session,
    sections: session.sections.filter((s) => s.sectionKey === sectionKey),
    comments: session.comments.filter((c) => commentSectionKey(c) === sectionKey),
    annotations: session.annotations.filter((a) => a.sectionKey === sectionKey),
  };
}
