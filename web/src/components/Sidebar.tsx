import { useState } from 'react';
import { unreadCount, useUnread } from '../lib/unread';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { ChapterPanel } from './ChapterPanel';
import { CommentsPanel } from './CommentsPanel';
import { FileTreePanel } from './FileTreePanel';
import { TodoPanel } from './TodoPanel';
import { TurnActivityPanel } from './TurnActivityPanel';

function TreeIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M2 3.5h5M2 8h3M7 8h7M7 12.5h7M2 3.5v9M2 8h.01"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

function ActivityIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M1.5 8h3l2-5 3 10 2-5h3"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function CommentIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M14 8a6 6 0 1 1-2.2-4.65L14 2.5l-.4 2.6A5.97 5.97 0 0 1 14 8z"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
        transform="rotate(180 8 8) scale(1 -1) translate(0 -16)"
      />
      <path
        d="M13.5 8a5.5 5.5 0 1 1-2-4.24L13.5 2l-.35 2.4c.55.9.85 1.95.85 3.1z"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function Sidebar({
  session,
  onSelectFile,
  onSelectComment,
}: {
  session: SessionResponse;
  onSelectFile(path: string): void;
  onSelectComment(comment: Comment): void;
}) {
  const [tab, setTab] = useState<'files' | 'comments' | 'activity'>('files');
  const { seen } = useUnread();
  const { viewMode } = useViewPrefs();
  const unread = unreadCount(session.comments, seen);
  const organization = viewMode !== 'default' ? session.organization : null;

  return (
    <>
      <div className="sidebar-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'files'}
          title="Files"
          onClick={() => setTab('files')}
        >
          <TreeIcon />
        </button>
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'comments'}
          title="Comments"
          onClick={() => setTab('comments')}
        >
          <CommentIcon />
          {unread > 0 ? <span className="tab-badge">{unread}</span> : null}
        </button>
        <button
          type="button"
          role="tab"
          className="tab-btn"
          aria-selected={tab === 'activity'}
          title="Turn activity"
          onClick={() => setTab('activity')}
        >
          <ActivityIcon />
        </button>
      </div>
      {tab === 'activity' ? (
        <TurnActivityPanel session={session} />
      ) : tab === 'files' ? (
        organization ? (
          viewMode === 'todo' ? (
            <TodoPanel session={session} organization={organization} onSelectFile={onSelectFile} />
          ) : (
            <ChapterPanel
              session={session}
              organization={organization}
              onSelectFile={onSelectFile}
            />
          )
        ) : (
          <FileTreePanel
            files={session.files}
            fileStates={session.fileStates}
            organization={session.organization}
            onSelectFile={onSelectFile}
          />
        )
      ) : (
        <CommentsPanel session={session} onSelectComment={onSelectComment} />
      )}
    </>
  );
}
