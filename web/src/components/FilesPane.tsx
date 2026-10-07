import { useState } from 'react';
import type { FileRef } from '../lib/diff/items';
import { isOpenHumanThread } from '../lib/stack';
import { fileThreads } from '../lib/threads';
import { unreadCount, useUnread } from '../lib/unread';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { ChapterPanel } from './ChapterPanel';
import { CommentsPanel } from './CommentsPanel';
import { FilesPaneResizer } from './FilesPaneResizer';
import { FileTreePanel } from './FileTreePanel';
import { TodoPanel } from './TodoPanel';

type PaneTab = 'files' | 'threads';

export function FilesPane({
  session,
  onSelectFile,
  onSelectComment,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
  onSelectComment(comment: Comment): void;
}) {
  const [tab, setTab] = useState<PaneTab>('files');
  const { seen } = useUnread();
  const { viewMode } = useViewPrefs();
  const threads = fileThreads(session.comments);
  const unread = unreadCount(threads, seen);
  const open = threads.filter(isOpenHumanThread).length;
  const organized = viewMode !== 'default' && session.sections.some((s) => s.organization !== null);

  return (
    <aside className="files-pane" aria-label="Files">
      <div className="seg seg-full" role="tablist" aria-label="File pane">
        <button type="button" role="tab" className="seg-btn" aria-selected={tab === 'files'} onClick={() => setTab('files')}>
          Files
        </button>
        <button type="button" role="tab" className="seg-btn" aria-selected={tab === 'threads'} onClick={() => setTab('threads')}>
          Threads
          {open > 0 ? <span className={`count${unread > 0 ? ' count-unread' : ''}`}>{open}</span> : null}
        </button>
      </div>
      {tab === 'threads' ? (
        <CommentsPanel session={session} onSelectComment={onSelectComment} />
      ) : organized ? (
        viewMode === 'todo' ? (
          <TodoPanel session={session} onSelectFile={onSelectFile} />
        ) : (
          <ChapterPanel session={session} onSelectFile={onSelectFile} />
        )
      ) : (
        <FileTreePanel session={session} onSelectFile={onSelectFile} />
      )}
      <FilesPaneResizer />
    </aside>
  );
}
