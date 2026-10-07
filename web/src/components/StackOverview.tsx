import { stackRows } from '../lib/stack';
import { useUnread } from '../lib/unread';
import type { SessionResponse } from '../lib/types';
import { CiIcon } from './StackRail';
import { Icon } from './ui/Icon';

function stateLabel(state: string, draft: boolean): string {
  if (draft) return 'Draft';
  return state.charAt(0) + state.slice(1).toLowerCase();
}

export function StackOverview({
  session,
  onScope,
}: {
  session: SessionResponse;
  onScope(sectionKey: string): void;
}) {
  const { seen } = useUnread();
  const rows = stackRows(session, seen);
  const prs = session.review.kind === 'pr';
  return (
    <div className="overview">
      <table className="stack-table">
        <thead>
          <tr>
            <th>{prs ? 'Pull request' : 'Branch'}</th>
            <th className="num">Files</th>
            <th className="num">Lines</th>
            {prs ? <th>State</th> : null}
            {prs ? <th>Checks</th> : null}
            <th className="num">Reviewed</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.sectionKey}>
              <td className="stack-table-title">
                <button type="button" className="link-button" title={row.title} onClick={() => onScope(row.sectionKey)}>
                  {row.pr ? <span className="dim">#{row.pr.number}</span> : null}
                  <span className="ellipsis">{row.title}</span>
                </button>
                {row.threads > 0 ? (
                  <span className="rail-row-threads" title={`${row.threads} open threads`}>
                    <Icon name="comment" size={12} />
                    {row.threads}
                  </span>
                ) : null}
              </td>
              <td className="num">{row.total}</td>
              <td className="num">
                <span className="stat-add">+{row.additions}</span> <span className="stat-del">−{row.deletions}</span>
              </td>
              {prs ? (
                <td>
                  {row.pr ? (
                    <span className={`state-badge state-${row.pr.draft ? 'draft' : row.pr.state.toLowerCase()}`}>
                      {stateLabel(row.pr.state, row.pr.draft)}
                    </span>
                  ) : null}
                </td>
              ) : null}
              {prs ? (
                <td>
                  <CiIcon state={row.ci} />
                </td>
              ) : null}
              <td className="num">
                {row.reviewed}/{row.total}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
