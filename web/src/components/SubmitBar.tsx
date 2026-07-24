import { useClose, useSubmit } from '../lib/api';
import { useReview } from '../lib/review-context';
import { STATUS_NOTICES } from '../lib/status';
import type { SessionResponse } from '../lib/types';

export function SubmitBar({ session }: { session: SessionResponse }) {
  const { slug } = useReview();
  const submit = useSubmit(slug);
  const close = useClose(slug);

  const status = session.review.status;
  // Claude-authored comments are informational annotations, not reviewer TODOs.
  const openCount = session.comments.filter(
    (c) => c.status === 'open' && c.origin !== 'claude',
  ).length;
  const frozenPath = session.feedbackPath ?? submit.data?.feedbackPath ?? null;
  const total = session.sections.reduce((n, s) => n + s.files.length, 0);
  const reviewedCount = session.sections.reduce(
    (n, s) => n + s.files.filter((f) => s.fileStates[f.path]?.reviewed).length,
    0,
  );
  const branchCount = session.sections.filter((s) => !s.pending).length;

  return (
    <header className="submit-bar">
      <div className="meta">
        <strong className="brand">cc-review</strong>
        <span className="branch">{session.review.branch}</span>
        <span className="dim">v{session.version}</span>
        {branchCount > 1 ? <span className="dim">{branchCount} branches</span> : null}
        <span className="dim">{total} files</span>
        <span className="dim">{openCount} open</span>
        <span className="dim">
          {reviewedCount}/{total} reviewed
        </span>
        <span className="progress-track">
          <span
            className="progress-fill"
            style={{ width: `${total > 0 ? (reviewedCount / total) * 100 : 0}%` }}
          />
        </span>
        <span className={`status status-${status}`}>{status}</span>
      </div>
      <div className="actions">
        {status === 'open' ? (
          <>
            <button
              type="button"
              disabled={close.isPending}
              onClick={() => close.mutate()}
            >
              {close.isPending ? 'Closing…' : 'Close without submitting'}
            </button>
            <button
              type="button"
              className="primary"
              disabled={submit.isPending}
              onClick={() => submit.mutate()}
            >
              {submit.isPending ? 'Submitting…' : 'Submit review'}
            </button>
          </>
        ) : (
          <>
            <span className="frozen">
              {status === 'submitted'
                ? frozenPath
                  ? `Submitted → ${frozenPath}`
                  : 'Submitted'
                : STATUS_NOTICES[status]}
            </span>
            {status === 'expired' && (
              <button type="button" disabled={close.isPending} onClick={() => close.mutate()}>
                {close.isPending ? 'Closing…' : 'Close'}
              </button>
            )}
          </>
        )}
      </div>
    </header>
  );
}
