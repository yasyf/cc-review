import { getRouteApi } from '@tanstack/react-router';
import type { FileRef } from '../lib/diff/items';
import { useSidebarLayout } from '../lib/sidebar-layout';
import type { SidebarTab } from '../lib/sidebar-layout';
import { unreadCount, useUnread } from '../lib/unread';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { ChapterPanel } from './ChapterPanel';
import { CommentsPanel } from './CommentsPanel';
import { FileTreePanel } from './FileTreePanel';
import { SidebarResizer } from './SidebarResizer';
import { TodoPanel } from './TodoPanel';
import { TurnActivityPanel } from './TurnActivityPanel';
import { Icon } from './ui/Icon';
import type { IconName } from './ui/icons';

const routeApi = getRouteApi('/s/$slug');

const TABS: { id: SidebarTab; label: string; icon: IconName }[] = [
  { id: 'files', label: 'Files', icon: 'tree' },
  { id: 'comments', label: 'Comments', icon: 'comment' },
  { id: 'activity', label: 'Activity', icon: 'activity' },
];

export function Sidebar({
  session,
  onSelectFile,
  onSelectComment,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
  onSelectComment(comment: Comment): void;
}) {
  const search = routeApi.useSearch();
  const tab = search.tab === 'conversation' && session.review.kind !== 'pr' ? 'files' : (search.tab ?? 'files');
  const navigate = routeApi.useNavigate();
  const { mode, dismissOverlay } = useSidebarLayout();
  const { seen } = useUnread();
  const { viewMode } = useViewPrefs();
  const unread = unreadCount(session.comments, seen);
  const organized = viewMode !== 'default' && session.sections.some((s) => s.organization !== null);

  function setTab(next: SidebarTab) {
    void navigate({
      search: ({ tab: _previous, ...rest }) => (next === 'files' ? rest : { ...rest, tab: next }),
      replace: true,
    });
  }

  function selectFile(ref: FileRef) {
    onSelectFile(ref);
    dismissOverlay();
  }

  function selectComment(comment: Comment) {
    onSelectComment(comment);
    dismissOverlay();
  }

  return (
    <>
      <div className="sidebar-tabs" role="tablist" aria-label="Sidebar">
        {TABS.map(({ id, label, icon }) => (
          <button
            key={id}
            type="button"
            role="tab"
            className="tab-btn"
            aria-selected={tab === id}
            onClick={() => setTab(id)}
          >
            <Icon name={icon} size={14} />
            {label}
            {id === 'comments' && unread > 0 ? <span className="tab-badge">{unread}</span> : null}
          </button>
        ))}
      </div>
      {tab === 'activity' ? (
        <TurnActivityPanel session={session} />
      ) : tab === 'files' ? (
        organized ? (
          viewMode === 'todo' ? (
            <TodoPanel session={session} onSelectFile={selectFile} />
          ) : (
            <ChapterPanel session={session} onSelectFile={selectFile} />
          )
        ) : (
          <FileTreePanel session={session} onSelectFile={selectFile} />
        )
      ) : (
        <CommentsPanel session={session} onSelectComment={selectComment} />
      )}
      {mode === 'docked' ? <SidebarResizer /> : null}
    </>
  );
}
