import { stackRows, trunkBranch } from '../lib/stack';
import type { CiState, ReviewState, StackRow } from '../lib/stack';
import { useLayout } from '../lib/layout';
import { useUnread } from '../lib/unread';
import type { SessionResponse } from '../lib/types';
import { Icon } from './ui/Icon';
import type { IconName } from './ui/icons';

const CI_ICON: Record<CiState, { icon: IconName; label: string }> = {
  success: { icon: 'check-circle', label: 'Checks passing' },
  failure: { icon: 'x-circle', label: 'Checks failing' },
  pending: { icon: 'pending-circle', label: 'Checks running' },
  none: { icon: 'circle', label: 'No checks' },
};

const REVIEW_ICON: Record<Exclude<ReviewState, 'none'>, { icon: IconName; label: string }> = {
  approved: { icon: 'check', label: 'Approved' },
  changes_requested: { icon: 'request-changes', label: 'Changes requested' },
  commented: { icon: 'comment', label: 'Commented' },
  pending: { icon: 'eye', label: 'Review requested' },
};

export function CiIcon({ state }: { state: CiState }) {
  const { icon, label } = CI_ICON[state];
  return (
    <span className={`ci-icon ci-${state}`} role="img" aria-label={label} title={label}>
      <Icon name={icon} size={14} />
    </span>
  );
}

function Row({ row, active, onSelect }: { row: StackRow; active: boolean; onSelect(): void }) {
  const review = row.review === 'none' ? null : REVIEW_ICON[row.review];
  return (
    <button
      type="button"
      className="rail-row"
      aria-current={active || undefined}
      title={row.pr ? `#${row.pr.number} ${row.title}` : row.title}
      onClick={onSelect}
    >
      <span className="rail-row-line">
        <span className="rail-node" aria-hidden="true" />
        <span className="rail-row-title">{row.title}</span>
      </span>
      <span className="rail-row-meta">
        {row.pr ? <CiIcon state={row.ci} /> : null}
        {row.pr ? <span className="rail-row-number">#{row.pr.number}</span> : <code className="rail-row-branch">{row.branch || 'working tree'}</code>}
        {review ? (
          <span className={`review-icon review-${row.review}`} role="img" aria-label={review.label} title={review.label}>
            <Icon name={review.icon} size={14} />
          </span>
        ) : null}
        {row.threads > 0 ? (
          <span
            className={`rail-row-threads${row.unread > 0 ? ' rail-row-threads-unread' : ''}`}
            title={`${row.threads} open thread${row.threads === 1 ? '' : 's'}`}
          >
            <Icon name="comment" size={12} />
            {row.threads}
          </span>
        ) : null}
        <span className="rail-row-count">
          {row.reviewed}/{row.total}
        </span>
      </span>
      <span
        className="rail-row-progress"
        role="progressbar"
        aria-label="Files reviewed"
        aria-valuemin={0}
        aria-valuemax={row.total}
        aria-valuenow={row.reviewed}
      >
        <span style={{ width: `${row.total > 0 ? (row.reviewed / row.total) * 100 : 0}%` }} />
      </span>
    </button>
  );
}

export function StackRail({
  session,
  scope,
  onScope,
}: {
  session: SessionResponse;
  scope: string | undefined;
  onScope(sectionKey: string | undefined): void;
}) {
  const { seen } = useUnread();
  const { dismissRail } = useLayout();
  const rows = stackRows(session, seen);
  const trunk = trunkBranch(session);
  const reviewed = rows.reduce((n, r) => n + r.reviewed, 0);
  const total = rows.reduce((n, r) => n + r.total, 0);

  function select(sectionKey: string | undefined) {
    onScope(sectionKey);
    dismissRail();
  }

  return (
    <nav className="rail" aria-label="Stack">
      <div className="rail-head">
        <span>Stack</span>
        <span className="dim">
          {rows.length} {session.review.kind === 'pr' ? 'PRs' : 'branches'}
        </span>
      </div>
      <div className="rail-list">
        <button
          type="button"
          className="rail-row rail-row-all"
          aria-current={scope === undefined || undefined}
          onClick={() => select(undefined)}
        >
          <span className="rail-row-line">
            <Icon name="tree" size={14} />
            <span className="rail-row-title">Whole stack</span>
            <span className="rail-row-count">
              {reviewed}/{total}
            </span>
          </span>
        </button>
        <div className="rail-stack">
          {rows.map((row) => (
            <Row
              key={row.sectionKey}
              row={row}
              active={scope === row.sectionKey}
              onSelect={() => select(row.sectionKey)}
            />
          ))}
          {trunk ? (
            <div className="rail-trunk">
              <span className="rail-node rail-node-trunk" aria-hidden="true" />
              <Icon name="branch" size={12} />
              <code>{trunk}</code>
            </div>
          ) : null}
        </div>
      </div>
    </nav>
  );
}
