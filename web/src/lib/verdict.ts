import type { PullRequest, Section } from './types';

export type Verdict = 'COMMENT' | 'APPROVE' | 'REQUEST_CHANGES';

export interface VerdictOption {
  value: Verdict;
  label: string;
  disabledReason: string | null;
}

export function verdictOptions(
  sections: readonly Section[],
  pullRequests: readonly PullRequest[],
): VerdictOption[] {
  const shown = new Set(sections.map((section) => section.prNumber));
  const own = pullRequests
    .filter((pr) => pr.viewerIsAuthor && shown.has(pr.number))
    .map((pr) => `#${pr.number}`);
  const ownReason =
    own.length === 0
      ? null
      : `You authored ${own.join(', ')}; GitHub does not allow approving or requesting changes on your own pull request.`;
  return [
    { value: 'COMMENT', label: 'Comment', disabledReason: null },
    { value: 'APPROVE', label: 'Approve', disabledReason: ownReason },
    { value: 'REQUEST_CHANGES', label: 'Request changes', disabledReason: ownReason },
  ];
}
