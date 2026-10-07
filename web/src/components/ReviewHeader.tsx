import { useState } from 'react';
import { ciRollup, reviewRollup, sectionPullRequest } from '../lib/stack';
import type { CiState } from '../lib/stack';
import type { PullRequest, PullRequestCheck, PullRequestReviewer, SessionResponse } from '../lib/types';
import { Markdown } from './ui/Markdown';
import { PopoverButton } from './PopoverButton';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';

const CI_SUMMARY: Record<CiState, string> = {
  success: 'All checks passed',
  failure: 'Some checks failed',
  pending: 'Checks running',
  none: 'No checks',
};

const REVIEWER_STATE: Record<PullRequestReviewer['state'], string> = {
  APPROVED: 'approved',
  CHANGES_REQUESTED: 'requested changes',
  COMMENTED: 'commented',
  PENDING: 'review requested',
};

function avatarUrl(login: string): string {
  return `https://github.com/${encodeURIComponent(login)}.png?size=40`;
}

function prState(pr: PullRequest): { label: string; tone: string } {
  if (pr.draft) return { label: 'Draft', tone: 'draft' };
  return { label: pr.state.charAt(0) + pr.state.slice(1).toLowerCase(), tone: pr.state.toLowerCase() };
}

function CheckRow({ check }: { check: PullRequestCheck }) {
  return (
    <li className="check-row">
      <span className={`ci-dot ci-${ciRollup([check])}`} aria-hidden="true" />
      {check.url ? (
        <a href={check.url} target="_blank" rel="noreferrer">
          {check.name}
        </a>
      ) : (
        <span>{check.name}</span>
      )}
      <span className="check-state">{check.state.toLowerCase()}</span>
    </li>
  );
}

function ChecksRollup({ checks }: { checks: readonly PullRequestCheck[] }) {
  const ci = ciRollup(checks);
  const passed = checks.filter((c) => c.state === 'SUCCESS').length;
  return (
    <PopoverButton
      className="pr-checks"
      popoverLabel="Checks"
      label={
        <>
          <span className={`ci-dot ci-${ci}`} aria-hidden="true" />
          {CI_SUMMARY[ci]}
          {checks.length > 0 ? (
            <span className="dim">
              {passed}/{checks.length}
            </span>
          ) : null}
        </>
      }
    >
      {checks.length === 0 ? (
        <div className="dim">No checks reported for this head.</div>
      ) : (
        <ul className="check-list">
          {checks.map((check) => (
            <CheckRow key={check.name} check={check} />
          ))}
        </ul>
      )}
    </PopoverButton>
  );
}

function Reviewers({ reviewers }: { reviewers: readonly PullRequestReviewer[] }) {
  if (reviewers.length === 0) return <span className="dim">No reviewers</span>;
  return (
    <ul className="pr-reviewers">
      {reviewers.map((r) => (
        <li key={r.login} className={`pr-reviewer review-${reviewRollup([r])}`}>
          <img className="avatar-img" src={r.avatarUrl} alt="" width={20} height={20} />
          <span>{r.login}</span>
          <span className="pr-reviewer-state">{REVIEWER_STATE[r.state]}</span>
        </li>
      ))}
    </ul>
  );
}

function PrHeader({ pr }: { pr: PullRequest }) {
  const [expanded, setExpanded] = useState(false);
  const state = prState(pr);
  return (
    <section className="pr-header">
      <div className="pr-header-title">
        <h1>
          {pr.title} <span className="pr-number">#{pr.number}</span>
        </h1>
        <a className="btn btn-secondary btn-sm" href={pr.url} target="_blank" rel="noreferrer">
          <Icon name="external" size={14} />
          Open on GitHub
        </a>
      </div>
      <div className="pr-header-meta">
        <span className={`pr-state pr-state-${state.tone}`}>{state.label}</span>
        <span className="pr-author">
          <img className="avatar-img" src={avatarUrl(pr.authorLogin)} alt="" width={20} height={20} />
          {pr.authorLogin}
        </span>
        <span className="pr-refs">
          <code>{pr.baseRefName}</code> ← <code>{pr.headRefName}</code>
        </span>
        <ChecksRollup checks={pr.checks} />
        <Reviewers reviewers={pr.reviewers} />
      </div>
      {pr.body.trim() ? (
        <div className={`pr-description${expanded ? ' pr-description-open' : ''}`}>
          <Markdown source={pr.body} />
          <Button size="sm" variant="ghost" className="pr-description-toggle" onClick={() => setExpanded(!expanded)}>
            {expanded ? 'Show less' : 'Show full description'}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

function BranchHeader({ session, scope }: { session: SessionResponse; scope: string | undefined }) {
  const section =
    session.sections.length === 1
      ? session.sections[0]
      : session.sections.find((s) => s.sectionKey === scope);
  if (!section) return null;
  return (
    <section className="branch-header">
      <code>{section.pending ? 'Working tree' : section.branch}</code>
      {section.parentBranch ? (
        <span className="dim">
          ← <code>{section.parentBranch}</code>
        </span>
      ) : null}
      {!section.pending && section.baseRef && section.headRef ? (
        <span className="dim">
          {section.baseRef.slice(0, 7)}..{section.headRef.slice(0, 7)}
        </span>
      ) : null}
    </section>
  );
}

export function ReviewHeader({ session, scope }: { session: SessionResponse; scope: string | undefined }) {
  if (session.review.kind === 'local') return <BranchHeader session={session} scope={scope} />;
  const section = session.sections.find((s) => s.sectionKey === scope);
  const pr = section
    ? sectionPullRequest(section, session.pullRequests)
    : session.pullRequests.find((p) => p.number === session.review.prNumber);
  return pr ? <PrHeader pr={pr} /> : null;
}
