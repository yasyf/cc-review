import { useSubmit } from '../lib/api';
import { useReview } from '../lib/review-context';
import type { SessionResponse } from '../lib/types';

export function SubmitBar({ session }: { session: SessionResponse }) {
  const { slug } = useReview();
  const submit = useSubmit(slug);

  const submitted = session.review.status === 'submitted';
  // Claude-authored comments are informational annotations, not reviewer TODOs.
  const openCount = session.comments.filter(
    (c) => c.status === 'open' && c.origin !== 'claude',
  ).length;
  const frozenPath = session.feedbackPath ?? submit.data?.feedbackPath ?? null;
  const total = session.files.length;
  const reviewedCount = session.files.filter((f) => session.fileStates[f.path]?.reviewed).length;

  return (
    <header className="submit-bar">
      <div className="meta">
        <strong className="brand">cc-review</strong>
        <span className="branch">{session.review.branch}</span>
        <span className="dim">v{session.version}</span>
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
        <span className={`status status-${session.review.status}`}>{session.review.status}</span>
      </div>
      <div className="actions">
        {submitted ? (
          <span className="frozen">{frozenPath ? `Submitted → ${frozenPath}` : 'Submitted'}</span>
        ) : (
          <button
            type="button"
            className="primary"
            disabled={submit.isPending}
            onClick={() => submit.mutate()}
          >
            {submit.isPending ? 'Submitting…' : 'Submit review'}
          </button>
        )}
      </div>
    </header>
  );
}
