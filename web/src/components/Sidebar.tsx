import { useState } from 'react';
import type { FileRef } from '../lib/diff';
import { unreadCount, useUnread } from '../lib/unread';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { ChapterPanel } from './ChapterPanel';
import { CommentsPanel } from './CommentsPanel';
import { FileTreePanel } from './FileTreePanel';
import { TodoPanel } from './TodoPanel';
import { TurnActivityPanel } from './TurnActivityPanel';
import { Icon } from './ui/Icon';

export function Sidebar({
  session,
  onSelectFile,
  onSelectComment,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
  onSelectComment(comment: Comment): void;
}) {
  const [tab, setTab] = useState<'files' | 'comments' | 'activity'>('files');
  const { seen } = useUnread();
  const { viewMode } = useViewPrefs();
  const unread = unreadCount(session.comments, seen);
  const organized = viewMode !== 'default' && session.sections.some((s) => s.organization !== null);

  return (
    <>
      <div className="sidebar-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'files'}
          onClick={() => setTab('files')}
        >
          <Icon name="tree" />
          Files
        </button>
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'comments'}
          onClick={() => setTab('comments')}
        >
          <Icon name="comment" />
          Comments
          {unread > 0 ? <span className="tab-badge">{unread}</span> : null}
        </button>
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'activity'}
          onClick={() => setTab('activity')}
        >
          <Icon name="activity" />
          Activity
        </button>
      </div>
      {tab === 'activity' ? (
        <TurnActivityPanel session={session} />
      ) : tab === 'files' ? (
        organized ? (
          viewMode === 'todo' ? (
            <TodoPanel session={session} onSelectFile={onSelectFile} />
          ) : (
            <ChapterPanel session={session} onSelectFile={onSelectFile} />
          )
        ) : (
          <FileTreePanel session={session} onSelectFile={onSelectFile} />
        )
      ) : (
        <CommentsPanel session={session} onSelectComment={onSelectComment} />
      )}
    </>
  );
}
