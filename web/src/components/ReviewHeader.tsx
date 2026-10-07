import { useRef, useState } from 'react';
import { ciRollup } from '../lib/stack';
import type { CiState } from '../lib/stack';
import type { PullRequest, PullRequestCheck, PullRequestReviewer, Section, SessionResponse } from '../lib/types';
import { CiIcon } from './StackRail';
import { Icon } from './ui/Icon';
import { Popover } from './ui/Popover';

const CI_SUMMARY: Record<CiState, string> = {
  success: 'Checks passed',
  failure: 'Checks failed',
  pending: 'Checks running',
  none: 'No checks',
};

const REVIEWER_STATE: Record<PullRequestReviewer['state'], string> = {
  APPROVED: 'approved',
  CHANGES_REQUESTED: 'requested changes',
  COMMENTED: 'commented',
  PENDING: 'review requested',
};

export function avatarUrl(login: string): string {
  return `https://github.com/${encodeURIComponent(login.replace(/\[bot\]$/, ''))}.png?size=40`;
}

function prState(pr: PullRequest): { label: string; tone: string } {
  if (pr.draft) return { label: 'Draft', tone: 'draft' };
  return { label: pr.state.charAt(0) + pr.state.slice(1).toLowerCase(), tone: pr.state.toLowerCase() };
}

function checkState(check: PullRequestCheck): CiState {
  return check.state === 'SKIPPED' || check.state === 'NEUTRAL' ? 'none' : ciRollup([check]);
}

export function CheckList({ checks }: { checks: readonly PullRequestCheck[] }) {
  if (checks.length === 0) return <div className="dim">No checks reported for this head.</div>;
  const order: Record<CiState, number> = { failure: 0, pending: 1, success: 2, none: 3 };
  const sorted = [...checks].sort((a, b) => order[checkState(a)] - order[checkState(b)] || a.name.localeCompare(b.name));
  return (
    <ul className="check-list">
      {sorted.map((check, i) => (
        <li key={`${check.name}-${i}`} className="check-row">
          <CiIcon state={checkState(check)} />
          {check.url ? (
            <a href={check.url} target="_blank" rel="noreferrer">
              {check.name}
            </a>
          ) : (
            <span>{check.name}</span>
          )}
          <span className="check-state">{check.state.toLowerCase()}</span>
        </li>
      ))}
    </ul>
  );
}

export function ChecksPill({ checks }: { checks: readonly PullRequestCheck[] }) {
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const ci = ciRollup(checks);
  const passed = checks.filter((c) => c.state === 'SUCCESS').length;
  return (
    <>
      <button
        ref={anchor}
        type="button"
        className={`pill pill-ci pill-ci-${ci}`}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <CiIcon state={ci} />
        {CI_SUMMARY[ci]}
        {checks.length > 0 ? (
          <span className="dim">
            {passed}/{checks.length}
          </span>
        ) : null}
      </button>
      {open ? (
        <Popover anchor={anchor} label="Checks" className="checks-popover" onClose={() => setOpen(false)}>
          <CheckList checks={checks} />
        </Popover>
      ) : null}
    </>
  );
}

function ReviewerStack({ reviewers }: { reviewers: readonly PullRequestReviewer[] }) {
  if (reviewers.length === 0) return null;
  return (
    <span className="avatar-stack" aria-label="Reviewers">
      {reviewers.map((r) => (
        <img
          key={r.login}
          className={`avatar-img avatar-stacked reviewer-${r.state.toLowerCase()}`}
          src={r.avatarUrl || avatarUrl(r.login)}
          alt={r.login}
          title={`${r.login} ${REVIEWER_STATE[r.state]}`}
          width={20}
          height={20}
        />
      ))}
    </span>
  );
}

function PrHeader({ pr }: { pr: PullRequest }) {
  const state = prState(pr);
  return (
    <section className="review-header">
      <div className="review-header-title">
        <h1 title={pr.title}>
          {pr.title} <span className="review-header-number">#{pr.number}</span>
        </h1>
        <a className="btn btn-ghost btn-sm btn-icon" href={pr.url} target="_blank" rel="noreferrer" aria-label="Open on GitHub" title="Open on GitHub">
          <Icon name="external" />
        </a>
      </div>
      <div className="review-header-meta">
        <span className={`state-badge state-${state.tone}`}>{state.label}</span>
        <span className="review-header-author">
          <img className="avatar-img" src={avatarUrl(pr.authorLogin)} alt="" width={18} height={18} />
          {pr.authorLogin}
        </span>
        <code className="refs">
          {pr.baseRefName} ← {pr.headRefName}
        </code>
        <ChecksPill checks={pr.checks} />
        <ReviewerStack reviewers={pr.reviewers} />
      </div>
    </section>
  );
}

function BranchHeader({ section }: { section: Section }) {
  return (
    <section className="review-header">
      <div className="review-header-title">
        <h1>{section.pending ? 'Working tree' : section.branch}</h1>
      </div>
      <div className="review-header-meta">
        {section.parentBranch ? (
          <code className="refs">
            {section.parentBranch} ← {section.pending ? 'working tree' : section.branch}
          </code>
        ) : null}
        {!section.pending && section.baseRef && section.headRef ? (
          <code className="refs dim">
            {section.baseRef.slice(0, 7)}..{section.headRef.slice(0, 7)}
          </code>
        ) : null}
        <span className="dim">
          {section.files.length} file{section.files.length === 1 ? '' : 's'}
        </span>
      </div>
    </section>
  );
}

function StackHeader({ session }: { session: SessionResponse }) {
  const prs = session.review.kind === 'pr';
  const top = session.sections[session.sections.length - 1];
  return (
    <section className="review-header">
      <div className="review-header-title">
        <h1>{prs ? `Stack of ${session.sections.length} pull requests` : `Stack of ${session.sections.length} branches`}</h1>
      </div>
      <div className="review-header-meta">
        <code className="refs">
          {session.sections[0]?.parentBranch} ← {top?.pending ? 'working tree' : top?.branch}
        </code>
        <span className="dim">{session.sections.reduce((n, s) => n + s.files.length, 0)} files</span>
      </div>
    </section>
  );
}

export function ReviewHeader({
  session,
  section,
  pr,
}: {
  session: SessionResponse;
  section: Section | undefined;
  pr: PullRequest | null;
}) {
  if (pr) return <PrHeader pr={pr} />;
  if (section) return <BranchHeader section={section} />;
  return <StackHeader session={session} />;
}
