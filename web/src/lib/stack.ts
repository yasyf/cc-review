import { isAutomated } from './automation';
import { commentSectionKey } from './diff/items';
import { isUnread } from './unread';
import type { SeenMap } from './unread';
import type {
  Comment,
  PullRequest,
  PullRequestCheck,
  PullRequestReviewer,
  Section,
  SessionResponse,
} from './types';

export type CiState = 'success' | 'failure' | 'pending' | 'none';
export type ReviewState = 'approved' | 'changes_requested' | 'commented' | 'pending' | 'none';

export interface DiffStats {
  additions: number;
  deletions: number;
}

export interface StackRow extends DiffStats {
  sectionKey: string;
  title: string;
  branch: string;
  pr: PullRequest | null;
  ci: CiState;
  review: ReviewState;
  threads: number;
  unread: number;
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

export function isOpenHumanThread(comment: Comment): boolean {
  return comment.status === 'open' && comment.author !== 'claude' && !isAutomated(comment);
}

export function diffStats(patchText: string): DiffStats {
  let additions = 0;
  let deletions = 0;
  for (const line of patchText.split('\n')) {
    if (line.startsWith('+') && !line.startsWith('+++')) additions += 1;
    else if (line.startsWith('-') && !line.startsWith('---')) deletions += 1;
  }
  return { additions, deletions };
}

export function sectionTitle(section: Section, pr: PullRequest | null): string {
  return pr?.title ?? (section.pending ? 'Working tree' : section.branch);
}

export function stackRows(session: SessionResponse, seen: SeenMap): StackRow[] {
  return session.sections
    .map((section) => {
      const pr = sectionPullRequest(section, session.pullRequests);
      const comments = session.comments.filter((c) => commentSectionKey(c) === section.sectionKey);
      return {
        sectionKey: section.sectionKey,
        title: sectionTitle(section, pr),
        branch: section.branch,
        pr,
        ci: pr ? ciRollup(pr.checks) : 'none',
        review: pr ? reviewRollup(pr.reviewers) : 'none',
        threads: comments.filter(isOpenHumanThread).length,
        unread: comments.filter((c) => isUnread(c, seen)).length,
        reviewed: section.files.filter((f) => section.fileStates[f.path]?.reviewed).length,
        total: section.files.length,
        ...diffStats(section.patchText),
      };
    })
    .reverse();
}

export function trunkBranch(session: SessionResponse): string {
  const bottom = session.sections[0];
  if (!bottom) return '';
  return sectionPullRequest(bottom, session.pullRequests)?.baseRefName ?? bottom.parentBranch;
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
