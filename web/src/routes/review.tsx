import { getRouteApi } from '@tanstack/react-router';
import { useSession } from '../lib/api';
import { EventStreamProvider } from '../lib/events';
import { ReviewProvider, useReview } from '../lib/review-context';
import { DiffView } from '../components/DiffView';
import { NotificationsBar } from '../components/NotificationsBar';
import { SubmitBar } from '../components/SubmitBar';

const routeApi = getRouteApi('/s/$slug');

function ReviewContent() {
  const { slug, version } = useReview();
  const { data, isPending, error } = useSession(slug, version);

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
  const { slug } = routeApi.useParams();
  const search = routeApi.useSearch();

  return (
    <ReviewProvider value={{ slug, ...search }}>
      <EventStreamProvider slug={slug} {...search}>
        <ReviewContent />
      </EventStreamProvider>
    </ReviewProvider>
  );
}
