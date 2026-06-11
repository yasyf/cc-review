import { useMemo, useRef, useState } from 'react';
import { useSetFileStates } from '../lib/api';
import { conversationByFile } from '../lib/conversation';
import { useFlip } from '../lib/flip';
import { todoGroups } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { Organization, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { FileRow } from './FileRow';
import type { RowFile } from './FileRow';
import { HiddenFilesStrip } from './HiddenFilesStrip';

// Every entry renders as a direct sibling of one container so FLIP can animate
// rows across group boundaries.
type TodoEntry =
  | { kind: 'group-head'; key: string; title: string; reviewedCount: number; total: number }
  | { kind: 'file-row'; key: string; file: RowFile; reviewed: boolean }
  | { kind: 'done-head'; key: string; count: number };

// The stack-ranked living todo list: scariest chapters first, reviewed files
// sink into a collapsed Done section, unorganized patch files trail unsorted.
export function TodoPanel({
  session,
  organization,
  onSelectFile,
}: {
  session: SessionResponse;
  organization: Organization;
  onSelectFile(path: string): void;
}) {
  const { slug, version } = useReview();
  const { clearExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const [doneOpen, setDoneOpen] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  useFlip(listRef);

  const groups = useMemo(
    () => todoGroups(organization, new Set(session.files.map((f) => f.path))),
    [organization, session.files],
  );
  const conversations = useMemo(() => conversationByFile(session.comments), [session.comments]);

  const entries = useMemo<TodoEntry[]>(() => {
    const states = session.fileStates;
    const result: TodoEntry[] = [];
    const done: RowFile[] = [];

    const append = (key: string, title: string, files: RowFile[]) => {
      const visible = files.filter((f) => !states[f.path]?.hidden);
      if (visible.length === 0) return;
      const active = visible.filter((f) => !states[f.path]?.reviewed);
      done.push(...visible.filter((f) => states[f.path]?.reviewed));
      result.push({
        kind: 'group-head',
        key,
        title,
        reviewedCount: visible.length - active.length,
        total: visible.length,
      });
      for (const f of active) {
        result.push({ kind: 'file-row', key: `file:${f.path}`, file: f, reviewed: false });
      }
    };

    // Titles come from the model and are not unique-validated.
    const titleCounts = new Map<string, number>();
    for (const group of groups) {
      const n = titleCounts.get(group.title) ?? 0;
      titleCounts.set(group.title, n + 1);
      append(`group:${group.title}#${n}`, group.title, group.files);
    }

    const organized = new Set(groups.flatMap((g) => g.files.map((f) => f.path)));
    append(
      'head:unsorted',
      'Unsorted',
      session.files.filter((f) => !organized.has(f.path)).map((f) => ({ path: f.path })),
    );

    if (done.length > 0) {
      result.push({ kind: 'done-head', key: 'head:done', count: done.length });
      if (doneOpen) {
        for (const f of done) {
          result.push({ kind: 'file-row', key: `file:${f.path}`, file: f, reviewed: true });
        }
      }
    }
    return result;
  }, [groups, session.files, session.fileStates, doneOpen]);

  const reorganizing = session.aiRequests.some(
    (r) => r.status === 'pending' || r.status === 'working',
  );
  const hidden = session.files.filter((f) => session.fileStates[f.path]?.hidden);

  function toggleReviewed(path: string, reviewed: boolean) {
    if (!reviewed) clearExpandOverride(path);
    mutateStates([{ path, reviewed: !reviewed }]);
  }

  return (
    <>
      <div className={`todo-panel${reorganizing ? ' todo-reorganizing' : ''}`}>
        {reorganizing ? <div className="todo-banner">Claude is reorganizing…</div> : null}
        <div className="todo-list" ref={listRef}>
          {entries.map((entry) => {
            switch (entry.kind) {
              case 'group-head':
                return (
                  <header
                    key={entry.key}
                    data-flip-key={entry.key}
                    className="chapter-head todo-group-head"
                  >
                    <span className="chapter-title">{entry.title}</span>
                    <span className="chapter-progress">
                      {entry.reviewedCount}/{entry.total}
                    </span>
                  </header>
                );
              case 'done-head':
                return (
                  <button
                    key={entry.key}
                    data-flip-key={entry.key}
                    type="button"
                    className="todo-done-head"
                    aria-expanded={doneOpen}
                    onClick={() => setDoneOpen((open) => !open)}
                  >
                    <span aria-hidden="true">{doneOpen ? '▾' : '▸'}</span>
                    Done ({entry.count})
                  </button>
                );
              case 'file-row': {
                const convo = conversations.get(entry.file.path);
                return (
                  <div key={entry.key} data-flip-key={entry.key}>
                    <FileRow
                      file={entry.file}
                      reviewed={entry.reviewed}
                      commentCount={convo?.openCount ?? 0}
                      needsReply={convo?.needsReply ?? false}
                      onSelect={() => onSelectFile(entry.file.path)}
                      onToggle={() => toggleReviewed(entry.file.path, entry.reviewed)}
                    />
                  </div>
                );
              }
            }
          })}
        </div>
      </div>
      <HiddenFilesStrip files={hidden} />
    </>
  );
}
