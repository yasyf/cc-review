import { useRef, useState } from 'react';
import type { Dispatch, RefObject, SetStateAction } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { AppShell, ToastStack } from '@cc-interact/react';
import { useSession } from '../lib/api';
import { EventStreamProvider, useEventStream } from '../lib/events';
import { LocalRequestsProvider } from '../lib/local-requests';
import { ReviewProvider, useReview } from '../lib/review-context';
import { UnreadProvider } from '../lib/unread';
import { useKeyboardShortcuts } from '../lib/useKeyboardShortcuts';
import { ViewPrefsProvider } from '../lib/view-prefs';
import { SidebarFrame } from '../lib/sidebar-layout';
import { AiBar } from '../components/AiBar';
import { DiffToolbar } from '../components/DiffToolbar';
import { DiffView } from '../components/DiffView';
import { ReviewSkeleton } from '../components/ReviewSkeleton';
import type { DiffViewHandle } from '../lib/diff/useDiffHandle';
import { ShortcutHelp } from '../components/ShortcutHelp';
import { Sidebar } from '../components/Sidebar';
import { SubmitBar } from '../components/SubmitBar';
import { Button } from '../components/ui/Button';
import { EmptyState } from '../components/ui/EmptyState';

const routeApi = getRouteApi('/s/$slug');

function ShortcutLayer({
  diffRef,
  helpOpen,
  setHelpOpen,
}: {
  diffRef: RefObject<DiffViewHandle | null>;
  helpOpen: boolean;
  setHelpOpen: Dispatch<SetStateAction<boolean>>;
}) {
  useKeyboardShortcuts(diffRef, { helpOpen, setHelpOpen });
  return null;
}

function ReviewContent() {
  const { slug, version } = useReview();
  const { data, isPending, error, refetch, isRefetching } = useSession(slug, version);
  const { notifications, dismiss } = useEventStream();
  const diffRef = useRef<DiffViewHandle>(null);
  const [helpOpen, setHelpOpen] = useState(false);

  if (isPending) return <ReviewSkeleton />;
  if (error) {
    return (
      <EmptyState
        icon="alert"
        tone="danger"
        title="Couldn't load this review"
        action={
          <Button disabled={isRefetching} onClick={() => void refetch()}>
            {isRefetching ? 'Retrying…' : 'Retry'}
          </Button>
        }
      >
        {error.message}
      </EmptyState>
    );
  }

  return (
    <UnreadProvider reviewId={slug} comments={data.comments} prune={version === undefined}>
      <ViewPrefsProvider reviewId={slug} versionId={data.versionId}>
        <LocalRequestsProvider versionId={data.versionId}>
          <SidebarFrame>
            <ShortcutLayer diffRef={diffRef} helpOpen={helpOpen} setHelpOpen={setHelpOpen} />
            <AppShell
              header={<SubmitBar session={data} />}
              sidebar={
                <Sidebar
                  session={data}
                  onSelectFile={(ref) => diffRef.current?.scrollToFile(ref)}
                  onSelectComment={(comment) => diffRef.current?.scrollToComment(comment)}
                />
              }
              main={
                <>
                  <DiffToolbar session={data} />
                  <DiffView key={data.versionId} session={data} ref={diffRef} />
                </>
              }
              footer={<AiBar session={data} diffRef={diffRef} />}
            />
            <ToastStack notifications={notifications} onDismiss={dismiss} />
            <ShortcutHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
          </SidebarFrame>
        </LocalRequestsProvider>
      </ViewPrefsProvider>
    </UnreadProvider>
  );
}

export function ReviewView() {
  const { slug } = routeApi.useParams();
  const search = routeApi.useSearch();

  return (
    <ReviewProvider value={search.version === undefined ? { slug } : { slug, version: search.version }}>
      <EventStreamProvider subject={slug} scope={search.version}>
        <ReviewContent />
      </EventStreamProvider>
    </ReviewProvider>
  );
}
