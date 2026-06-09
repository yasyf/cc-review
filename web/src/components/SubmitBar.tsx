import { useSubmit } from '../lib/api';
import { useEventStream } from '../lib/events';
import { useReview } from '../lib/review-context';
import type { SessionResponse } from '../lib/types';

export function SubmitBar({ session }: { session: SessionResponse }) {
  const { reviewId, token } = useReview();
  const submit = useSubmit(reviewId, token);
  const { feedbackPath } = useEventStream();

  const submitted = session.review.status === 'submitted';
  const openCount = session.comments.filter((c) => c.status === 'open').length;
  const frozenPath = feedbackPath ?? submit.data?.feedbackPath ?? null;

  return (
    <header className="submit-bar">
      <div className="meta">
        <strong className="brand">cc-review</strong>
        <span className="branch">{session.review.branch}</span>
        <span className="dim">v{session.version}</span>
        <span className="dim">{session.files.length} files</span>
        <span className="dim">{openCount} open</span>
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
