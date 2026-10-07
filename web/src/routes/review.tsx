import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, RefObject, SetStateAction } from 'react';
import { getRouteApi } from '@tanstack/react-router';
import { AppShell, ToastStack } from '@cc-interact/react';
import { useSession } from '../lib/api';
import type { DiffViewHandle } from '../lib/diff/useDiffHandle';
import { EventStreamProvider, useEventStream } from '../lib/events';
import { LayoutFrame, useLayout } from '../lib/layout';
import type { MainTab } from '../lib/layout';
import { LocalRequestsProvider } from '../lib/local-requests';
import { ReviewProvider, useReview } from '../lib/review-context';
import { scopeSession, sectionPullRequest, sectionTitle } from '../lib/stack';
import type { PullRequest, Section, SessionResponse } from '../lib/types';
import { UnreadProvider } from '../lib/unread';
import { useKeyboardShortcuts } from '../lib/useKeyboardShortcuts';
import { ViewPrefsProvider } from '../lib/view-prefs';
import { AiBar } from '../components/AiBar';
import { DiffToolbar } from '../components/DiffToolbar';
import { DiffView } from '../components/DiffView';
import { FilesPane } from '../components/FilesPane';
import { OverviewPanel } from '../components/OverviewPanel';
import { CheckList, ReviewHeader } from '../components/ReviewHeader';
import { ReviewSkeleton } from '../components/ReviewSkeleton';
import { ReviewTabs } from '../components/ReviewTabs';
import type { TabSpec } from '../components/ReviewTabs';
import { ShortcutHelp } from '../components/ShortcutHelp';
import { StackOverview } from '../components/StackOverview';
import { StackRail } from '../components/StackRail';
import { TopBar } from '../components/TopBar';
import { TurnActivityPanel } from '../components/TurnActivityPanel';
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

function ReviewContent({ section }: { section: string | undefined }) {
  const { slug, version } = useReview();
  const { data, isPending, error, refetch, isRefetching } = useSession(slug, version);

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
  return <ReviewBody data={data} section={section} />;
}

function tabsFor(session: SessionResponse, scoped: SessionResponse, section: Section | undefined, pr: PullRequest | null): TabSpec[] {
  const files = scoped.sections.reduce((n, s) => n + s.files.length, 0);
  const tabs: TabSpec[] = [];
  if (pr || !section) tabs.push({ id: 'overview', label: 'Overview' });
  tabs.push({ id: 'files', label: 'Files changed', count: files });
  if (pr) tabs.push({ id: 'checks', label: 'Checks', count: pr.checks.length });
  if (session.turns.length > 0) tabs.push({ id: 'activity', label: 'Activity', count: session.turns.length });
  return tabs;
}

function withFilesTab(diffRef: RefObject<DiffViewHandle | null>, show: () => void): RefObject<DiffViewHandle | null> {
  const handle: DiffViewHandle = {
    scrollToFile: (ref) => {
      show();
      diffRef.current?.scrollToFile(ref);
    },
    scrollToComment: (comment) => {
      show();
      diffRef.current?.scrollToComment(comment);
    },
    focusNextFile: () => {
      show();
      diffRef.current?.focusNextFile();
    },
    focusPrevFile: () => {
      show();
      diffRef.current?.focusPrevFile();
    },
    toggleViewedCurrent: () => diffRef.current?.toggleViewedCurrent(),
    toggleCollapseCurrent: () => diffRef.current?.toggleCollapseCurrent(),
    focusNextComment: () => {
      show();
      diffRef.current?.focusNextComment();
    },
    focusPrevComment: () => {
      show();
      diffRef.current?.focusPrevComment();
    },
  };
  return { current: handle };
}

function StackDivider({ session, sectionKey }: { session: SessionResponse; sectionKey: string | null }) {
  const section = session.sections.find((s) => s.sectionKey === sectionKey) ?? session.sections[0];
  if (!section) return null;
  const pr = sectionPullRequest(section, session.pullRequests);
  return (
    <div className="stack-divider" aria-live="polite">
      {pr ? <span className="dim">#{pr.number}</span> : null}
      <span className="ellipsis">{sectionTitle(section, pr)}</span>
      <span className="dim">· {section.files.length} files</span>
    </div>
  );
}

function FilesTab({
  session,
  diffRef,
  nav,
  stacked,
  hidden,
}: {
  session: SessionResponse;
  diffRef: RefObject<DiffViewHandle | null>;
  nav: RefObject<DiffViewHandle | null>;
  stacked: boolean;
  hidden: boolean;
}) {
  const { filesOpen } = useLayout();
  const [currentSection, setCurrentSection] = useState<string | null>(null);
  return (
    <div className="tab-panel tab-panel-files" inert={hidden} data-hidden={hidden || undefined}>
      {filesOpen ? (
        <FilesPane
          session={session}
          onSelectFile={(ref) => nav.current?.scrollToFile(ref)}
          onSelectComment={(comment) => nav.current?.scrollToComment(comment)}
        />
      ) : null}
      <div className="diff-column">
        <DiffToolbar session={session} />
        {stacked ? <StackDivider session={session} sectionKey={currentSection} /> : null}
        <DiffView
          key={session.versionId}
          session={session}
          ref={diffRef}
          {...(stacked ? { onCurrentSection: setCurrentSection } : {})}
        />
      </div>
    </div>
  );
}

function ReviewBody({ data, section: sectionParam }: { data: SessionResponse; section: string | undefined }) {
  const { slug, version } = useReview();
  const { notifications, dismiss } = useEventStream();
  const navigate = routeApi.useNavigate();
  const search = routeApi.useSearch();
  const diffRef = useRef<DiffViewHandle>(null);
  const [helpOpen, setHelpOpen] = useState(false);

  const hasRail = data.sections.length > 1 || data.review.kind === 'pr';
  const scope = data.sections.some((s) => s.sectionKey === sectionParam) ? sectionParam : undefined;
  const section = scope !== undefined ? data.sections.find((s) => s.sectionKey === scope) : data.sections.length === 1 ? data.sections[0] : undefined;
  const pr = section ? sectionPullRequest(section, data.pullRequests) : null;
  const scoped = useMemo(() => scopeSession(data, section?.sectionKey), [data, section?.sectionKey]);
  const tabs = tabsFor(data, scoped, section, pr);
  const tab: MainTab = tabs.some((t) => t.id === search.tab) && search.tab ? search.tab : 'files';

  const setTab = useCallback(
    (next: MainTab) => {
      void navigate({
        search: ({ tab: _previous, ...rest }) => (next === 'files' ? rest : { ...rest, tab: next }),
        replace: true,
      });
    },
    [navigate],
  );
  const tabRef = useRef(tab);
  useEffect(() => {
    tabRef.current = tab;
  }, [tab]);
  const showFiles = useCallback(() => {
    if (tabRef.current !== 'files') setTab('files');
  }, [setTab]);
  const nav = useMemo(() => withFilesTab(diffRef, showFiles), [showFiles]);

  function setScope(next: string | undefined) {
    void navigate({
      search: ({ section: _previous, ...rest }) => (next === undefined ? rest : { ...rest, section: next }),
      replace: true,
    });
  }

  return (
    <UnreadProvider reviewId={slug} comments={data.comments} prune={version === undefined}>
      <ViewPrefsProvider reviewId={slug} versionId={data.versionId}>
        <LocalRequestsProvider versionId={data.versionId}>
          <LayoutFrame hasRail={hasRail}>
            <ShortcutLayer diffRef={nav} helpOpen={helpOpen} setHelpOpen={setHelpOpen} />
            <AppShell
              header={<TopBar session={data} hasRail={hasRail} />}
              {...(hasRail ? { sidebar: <StackRail session={data} scope={scope} onScope={setScope} /> } : {})}
              main={
                <>
                  <div className="review-head">
                    <ReviewHeader session={data} section={section} pr={pr} />
                    <ReviewTabs tabs={tabs} active={tab} onSelect={setTab} />
                  </div>
                  <div className="tab-stage">
                    <FilesTab
                      session={scoped}
                      diffRef={diffRef}
                      nav={nav}
                      stacked={section === undefined && data.sections.length > 1}
                      hidden={tab !== 'files'}
                    />
                    {tab === 'overview' ? (
                      <div className="tab-panel tab-panel-scroll">
                        {pr && section ? (
                          <OverviewPanel session={scoped} section={section} pr={pr} />
                        ) : (
                          <StackOverview session={data} onScope={setScope} />
                        )}
                      </div>
                    ) : null}
                    {tab === 'checks' && pr ? (
                      <div className="tab-panel tab-panel-scroll">
                        <div className="overview">
                          <CheckList checks={pr.checks} />
                        </div>
                      </div>
                    ) : null}
                    {tab === 'activity' ? (
                      <div className="tab-panel tab-panel-scroll">
                        <TurnActivityPanel session={data} />
                      </div>
                    ) : null}
                  </div>
                </>
              }
              footer={<AiBar session={data} diffRef={nav} />}
            />
            <ToastStack notifications={notifications} onDismiss={dismiss} />
            <ShortcutHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
          </LayoutFrame>
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
        <ReviewContent section={search.section} />
      </EventStreamProvider>
    </ReviewProvider>
  );
}
