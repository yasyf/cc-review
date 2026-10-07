import { Link } from '@tanstack/react-router';
import { useReviews } from '../lib/api';
import type { ReviewSummary } from '../lib/types';

function reviewTitle(review: ReviewSummary): string {
  if (review.title) return review.title;
  return review.branch || review.scope;
}

function relativeTime(iso: string): string {
  const minutes = Math.round((Date.now() - Date.parse(iso)) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

export function LandingView() {
  const { data, isPending, error } = useReviews();

  if (isPending) return <div className="state">Loading reviews…</div>;
  if (error) return <div className="state state-error">{error.message}</div>;

  return (
    <main className="landing">
      <h1 className="landing-title">Open reviews</h1>
      {data.length === 0 ? (
        <div className="landing-empty">
          No open reviews. Start one with <code>cc-review start</code> or{' '}
          <code>cc-review start --pr &lt;url&gt;</code>.
        </div>
      ) : (
        <ul className="landing-list">
          {data.map((review) => (
            <li key={review.id}>
              <Link className="landing-row" to="/s/$slug" params={{ slug: review.slug }}>
                <span className="landing-row-title">{reviewTitle(review)}</span>
                <span className="landing-row-repo">
                  {review.kind === 'pr' ? `${review.repo}#${review.prNumber}` : review.scope}
                </span>
                <span className={`status status-${review.status}`}>{review.status}</span>
                <span className="landing-row-time">{relativeTime(review.lastActivity)}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
