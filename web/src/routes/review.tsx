import { useRef, useState } from 'react';
import type { Dispatch, RefObject, SetStateAction } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { AppShell, NotificationsBar } from '@cc-interact/react';
import { useSession } from '../lib/api';
import { EventStreamProvider, useEventStream } from '../lib/events';
import { LocalRequestsProvider } from '../lib/local-requests';
import { ReviewProvider, useReview } from '../lib/review-context';
import { UnreadProvider } from '../lib/unread';
import { useKeyboardShortcuts } from '../lib/useKeyboardShortcuts';
import { ViewPrefsProvider } from '../lib/view-prefs';
import { AiBar } from '../components/AiBar';
import { DiffToolbar } from '../components/DiffToolbar';
import { DiffView } from '../components/DiffView';
import type { DiffViewHandle } from '../components/DiffView';
import { ShortcutHelp } from '../components/ShortcutHelp';
import { Sidebar } from '../components/Sidebar';
import { SubmitBar } from '../components/SubmitBar';

const routeApi = getRouteApi('/s/$slug');

// Renders nothing; binds the global shortcut listener from inside the provider
// tree so it can reach view prefs if needed.
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
  const { data, isPending, error } = useSession(slug, version);
  const { connected, notifications, dismiss } = useEventStream();
  const diffRef = useRef<DiffViewHandle>(null);
  const [helpOpen, setHelpOpen] = useState(false);

  if (isPending) return <div className="state">Loading review…</div>;
  if (error) return <div className="state state-error">{error.message}</div>;

  return (
    <UnreadProvider reviewId={slug} comments={data.comments} prune={version === undefined}>
      <ViewPrefsProvider reviewId={slug} versionId={data.versionId}>
        <LocalRequestsProvider versionId={data.versionId}>
          <ShortcutLayer diffRef={diffRef} helpOpen={helpOpen} setHelpOpen={setHelpOpen} />
          <AppShell
            header={<SubmitBar session={data} />}
            notifications={
              <NotificationsBar
                connected={connected}
                notifications={notifications}
                onDismiss={dismiss}
              />
            }
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
          <ShortcutHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
        </LocalRequestsProvider>
      </ViewPrefsProvider>
    </UnreadProvider>
  );
}

export function ReviewView() {
  const { slug } = routeApi.useParams();
  const search = routeApi.useSearch();

  return (
    <ReviewProvider value={{ slug, ...search }}>
      <EventStreamProvider subject={slug} scope={search.version}>
        <ReviewContent />
      </EventStreamProvider>
    </ReviewProvider>
  );
}
