import { useCallback, useMemo, useRef, useState } from 'react';
import type { CodeViewLineSelection } from '@pierre/diffs';
import { CodeView } from '@pierre/diffs/react';
import type { CodeViewHandle } from '@pierre/diffs/react';
import { buildItems, parseFiles } from '../lib/diff';
import type { ThreadMeta } from '../lib/diff';
import type { SessionResponse } from '../lib/types';
import { themes } from '../worker';
import { CommentComposer } from './CommentComposer';
import { CommentThread } from './CommentThread';

export function DiffView({ session }: { session: SessionResponse }) {
  const handle = useRef<CodeViewHandle<ThreadMeta>>(null);
  const [selection, setSelection] = useState<CodeViewLineSelection | null>(null);

  const readOnly = session.review.status === 'submitted';

  const files = useMemo(() => parseFiles(session.patchText), [session.patchText]);
  const items = useMemo(() => buildItems(files, session.comments), [files, session.comments]);

  const options = useMemo(
    () =>
      ({
        theme: themes,
        diffStyle: 'unified',
        stickyHeaders: true,
        enableLineSelection: !readOnly,
      }) as const,
    [readOnly],
  );

  const renderAnnotation = useCallback(
    (annotation: { metadata: ThreadMeta }) => <CommentThread commentId={annotation.metadata.commentId} />,
    [],
  );

  const activeItem = selection ? items.find((i) => i.id === selection.id) : undefined;
  const fileDiff = activeItem?.type === 'diff' ? activeItem.fileDiff : undefined;

  function closeComposer() {
    setSelection(null);
    handle.current?.clearSelectedLines();
  }

  return (
    <div className="diff">
      <CodeView<ThreadMeta>
        ref={handle}
        items={items}
        options={options}
        selectedLines={selection}
        onSelectedLinesChange={setSelection}
        renderAnnotation={renderAnnotation}
      />
      {selection && fileDiff && !readOnly ? (
        <CommentComposer
          selection={selection}
          fileDiff={fileDiff}
          versionId={session.versionId}
          onClose={closeComposer}
        />
      ) : null}
    </div>
  );
}
