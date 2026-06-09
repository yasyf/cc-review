import { getRouteApi } from '@tanstack/react-router';
import type { VersionKey } from '../lib/api';
import { useSession } from '../lib/api';
import { EventStreamProvider } from '../lib/events';
import { ReviewProvider, useReview } from '../lib/review-context';
import { DiffView } from '../components/DiffView';
import { NotificationsBar } from '../components/NotificationsBar';
import { SubmitBar } from '../components/SubmitBar';

const routeApi = getRouteApi('/s/$reviewId');

function ReviewContent() {
  const { reviewId, token, version } = useReview();
  const { data, isPending, error } = useSession(reviewId, token, version);

  if (isPending) return <div className="state">Loading review…</div>;
  if (error) return <div className="state state-error">{error.message}</div>;

  return (
    <div className="app">
      <SubmitBar session={data} />
      <NotificationsBar />
      <main className="main">
        <DiffView session={data} />
      </main>
    </div>
  );
}

export function ReviewView() {
  const { reviewId } = routeApi.useParams();
  const { t, version } = routeApi.useSearch();
  const versionKey: VersionKey = version ?? 'latest';

  return (
    <ReviewProvider value={{ reviewId, token: t, version: versionKey }}>
      <EventStreamProvider reviewId={reviewId} token={t} version={versionKey}>
        <ReviewContent />
      </EventStreamProvider>
    </ReviewProvider>
  );
}
