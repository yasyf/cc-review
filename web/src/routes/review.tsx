import { useRef } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { useSession } from '../lib/api';
import { EventStreamProvider } from '../lib/events';
import { ReviewProvider, useReview } from '../lib/review-context';
import { UnreadProvider } from '../lib/unread';
import { ViewPrefsProvider } from '../lib/view-prefs';
import { AiBar } from '../components/AiBar';
import { DiffToolbar } from '../components/DiffToolbar';
import { DiffView } from '../components/DiffView';
import type { DiffViewHandle } from '../components/DiffView';
import { NotificationsBar } from '../components/NotificationsBar';
import { Sidebar } from '../components/Sidebar';
import { SubmitBar } from '../components/SubmitBar';

const routeApi = getRouteApi('/s/$slug');

function ReviewContent() {
  const { slug, version } = useReview();
  const { data, isPending, error } = useSession(slug, version);
  const diffRef = useRef<DiffViewHandle>(null);

  if (isPending) return <div className="state">Loading review…</div>;
  if (error) return <div className="state state-error">{error.message}</div>;

  return (
    <div className="app">
      <SubmitBar session={data} />
      <NotificationsBar />
      <UnreadProvider reviewId={slug} comments={data.comments} prune={version === undefined}>
        <ViewPrefsProvider reviewId={slug} versionId={data.versionId}>
          <div className="body">
            <Sidebar
              session={data}
              onSelectFile={(path) => diffRef.current?.scrollToFile(path)}
              onSelectComment={(comment) => diffRef.current?.scrollToComment(comment)}
            />
            <main className="main">
              <DiffToolbar session={data} />
              <DiffView key={data.versionId} session={data} ref={diffRef} />
            </main>
          </div>
          <AiBar session={data} />
        </ViewPrefsProvider>
      </UnreadProvider>
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
