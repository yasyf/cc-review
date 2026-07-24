import { useMemo, useRef, useState } from 'react';
import { isOrganizing, userRequestInFlight } from '../lib/ai-requests';
import { useSetFileStates } from '../lib/api';
import { useFlip } from '@cc-interact/react';
import { conversationByItem } from '../lib/conversation';
import { fileItemId } from '../lib/diff';
import type { FileRef } from '../lib/diff';
import { sectionTodoGroups } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { FileRow } from './FileRow';
import type { RowFile } from './FileRow';
import { HiddenFilesStrip } from './HiddenFilesStrip';

// Every entry renders as a direct sibling of one container so FLIP can animate
// rows across group boundaries.
type TodoEntry =
  | { kind: 'group-head'; key: string; title: string; branch?: string; reviewedCount: number; total: number }
  | { kind: 'file-row'; key: string; sectionKey: string; branch?: string; file: RowFile; reviewed: boolean }
  | { kind: 'done-head'; key: string; count: number };

// The stack-ranked living todo list: one global scariest-first checklist across
// every section (rows carry their branch), reviewed files sink into a collapsed
// Done section, unorganized patch files trail unsorted per section.
export function TodoPanel({
  session,
  onSelectFile,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
}) {
  const { slug, version } = useReview();
  const { clearExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const [doneOpen, setDoneOpen] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  useFlip(listRef, { flipClass: 'todo-row-flip', movedClass: 'todo-row-moved' });

  const groups = useMemo(() => sectionTodoGroups(session.sections), [session.sections]);
  const conversations = useMemo(() => conversationByItem(session.comments), [session.comments]);
  const sectionByKey = useMemo(
    () => new Map(session.sections.map((s) => [s.sectionKey, s])),
    [session.sections],
  );
  const showBranch = session.sections.length > 1;

  const entries = useMemo<TodoEntry[]>(() => {
    const result: TodoEntry[] = [];
    const done: { sectionKey: string; branch: string; file: RowFile }[] = [];
    const organizedBySection = new Map<string, Set<string>>();

    const append = (key: string, title: string, sectionKey: string, branch: string, files: RowFile[]) => {
      const states = sectionByKey.get(sectionKey)?.fileStates ?? {};
      const visible = files.filter((f) => !states[f.path]?.hidden);
      if (visible.length === 0) return;
      const active = visible.filter((f) => !states[f.path]?.reviewed);
      done.push(
        ...visible.filter((f) => states[f.path]?.reviewed).map((file) => ({ sectionKey, branch, file })),
      );
      result.push({
        kind: 'group-head',
        key,
        title,
        ...(showBranch ? { branch } : {}),
        reviewedCount: visible.length - active.length,
        total: visible.length,
      });
      for (const f of active) {
        result.push({
          kind: 'file-row',
          key: `file:${sectionKey}:${f.path}`,
          sectionKey,
          ...(showBranch ? { branch } : {}),
          file: f,
          reviewed: false,
        });
      }
    };

    // Titles come from the model and are not unique-validated.
    const titleCounts = new Map<string, number>();
    for (const group of groups) {
      const n = titleCounts.get(group.title) ?? 0;
      titleCounts.set(group.title, n + 1);
      const organized = organizedBySection.get(group.sectionKey) ?? new Set<string>();
      for (const f of group.files) organized.add(f.path);
      organizedBySection.set(group.sectionKey, organized);
      append(`group:${group.sectionKey}:${group.title}#${n}`, group.title, group.sectionKey, group.branch, group.files);
    }

    for (const section of session.sections) {
      const organized = organizedBySection.get(section.sectionKey) ?? new Set<string>();
      const unsorted = section.files.filter((f) => !organized.has(f.path)).map((f) => ({ path: f.path }));
      append(`unsorted:${section.sectionKey}`, 'Unsorted', section.sectionKey, section.branch, unsorted);
    }

    if (done.length > 0) {
      result.push({ kind: 'done-head', key: 'head:done', count: done.length });
      if (doneOpen) {
        for (const d of done) {
          result.push({
            kind: 'file-row',
            key: `file:${d.sectionKey}:${d.file.path}`,
            sectionKey: d.sectionKey,
            ...(showBranch ? { branch: d.branch } : {}),
            file: d.file,
            reviewed: true,
          });
        }
      }
    }
    return result;
  }, [groups, session.sections, sectionByKey, doneOpen, showBranch]);

  const reorganizing = isOrganizing(session.aiRequests);
  const applyingUserRequest = userRequestInFlight(session.aiRequests);
  const hidden: FileRef[] = session.sections.flatMap((s) =>
    s.files
      .filter((f) => s.fileStates[f.path]?.hidden)
      .map((f) => ({ sectionKey: s.sectionKey, path: f.path })),
  );

  function toggleReviewed(sectionKey: string, path: string, reviewed: boolean) {
    if (!reviewed) clearExpandOverride(fileItemId(sectionKey, path));
    mutateStates([{ sectionKey, path, reviewed: !reviewed }]);
  }

  return (
    <>
      <div
        className={`todo-panel${
          reorganizing ? ' todo-reorganizing' : applyingUserRequest ? ' todo-applying' : ''
        }`}
      >
        {reorganizing ? (
          <div className="todo-banner">Claude is reorganizing…</div>
        ) : applyingUserRequest ? (
          <div className="todo-banner">Claude is applying your request…</div>
        ) : null}
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
                    <span className="chapter-title">
                      {entry.title}
                      {entry.branch ? <span className="row-branch">{entry.branch}</span> : null}
                    </span>
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
                const convo = conversations.get(fileItemId(entry.sectionKey, entry.file.path));
                return (
                  <div key={entry.key} data-flip-key={entry.key}>
                    <FileRow
                      file={entry.file}
                      reviewed={entry.reviewed}
                      commentCount={convo?.openCount ?? 0}
                      needsReply={convo?.needsReply ?? false}
                      {...(entry.branch ? { branch: entry.branch } : {})}
                      onSelect={() => onSelectFile({ sectionKey: entry.sectionKey, path: entry.file.path })}
                      onToggle={() => toggleReviewed(entry.sectionKey, entry.file.path, entry.reviewed)}
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
