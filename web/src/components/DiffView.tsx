import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { Ref } from 'react';
import type { CodeViewOptions, SelectedLineRange } from '@pierre/diffs';
import { CodeView } from '@pierre/diffs/react';
import type { CodeViewHandle } from '@pierre/diffs/react';
import { buildItems, parseFiles } from '../lib/diff';
import type { AnnotationMeta, ComposerDraft, ReviewItem } from '../lib/diff';
import { clearDraft, composerDraftKey } from '../lib/drafts';
import type { Comment, SessionResponse } from '../lib/types';
import { themes } from '../worker';
import { CommentThread } from './CommentThread';
import { InlineComposer } from './InlineComposer';

// The imperative surface the rest of the app uses to navigate the diff; only
// this component talks to @pierre/diffs directly.
export interface DiffViewHandle {
  scrollToFile(path: string): void;
  scrollToComment(comment: Comment): void;
}

export function DiffView({ session, ref }: { session: SessionResponse; ref?: Ref<DiffViewHandle> }) {
  const codeView = useRef<CodeViewHandle<AnnotationMeta>>(null);
  const seqRef = useRef(0);
  const [draft, setDraft] = useState<ComposerDraft | null>(null);

  const readOnly = session.review.status === 'submitted';

  const files = useMemo(() => parseFiles(session.patchText), [session.patchText]);
  const items = useMemo(
    () => buildItems(files, session.comments, draft),
    [files, session.comments, draft],
  );

  // The draft's typed text is intentionally NOT cleared here: replacing the
  // draft (new selection, other file) carries the in-progress comment along.
  const openDraft = useCallback((filePath: string, range: SelectedLineRange) => {
    setDraft({ filePath, range, seq: ++seqRef.current });
  }, []);

  const closeDraft = useCallback(() => {
    clearDraft(composerDraftKey);
    setDraft(null);
    codeView.current?.clearSelectedLines();
  }, []);

  // The submit bar can freeze the review while a draft is open; a composer on a
  // submitted review would post into a frozen feedback file.
  useEffect(() => {
    if (readOnly) closeDraft();
  }, [readOnly, closeDraft]);

  const options = useMemo<CodeViewOptions<AnnotationMeta>>(
    () => ({
      theme: themes,
      diffStyle: 'unified',
      stickyHeaders: true,
      enableLineSelection: !readOnly,
      enableGutterUtility: !readOnly,
      // The selection commit (pointer-up) is the single open/close authority —
      // never onSelectedLinesChange (fires per drag frame), and not the gutter
      // callback (the same pointer-up also commits the selection, which would
      // double-open). A null commit is the single-line unselect gesture.
      // closeDraft's clearSelectedLines cannot loop back here: it suppresses
      // notification.
      onLineSelected: (range: SelectedLineRange | null, context: { item: { id: string } }) => {
        if (range) openDraft(context.item.id, range);
        else closeDraft();
      },
      // Must stay non-null: the library only routes "+" pointer-downs into
      // gutter selection when this callback exists.
      onGutterUtilityClick: () => {},
    }),
    [readOnly, openDraft, closeDraft],
  );

  const renderAnnotation = useCallback(
    (annotation: { metadata: AnnotationMeta }, item: ReviewItem) => {
      if (annotation.metadata.kind === 'thread') {
        return <CommentThread commentId={annotation.metadata.commentId} />;
      }
      if (!draft || item.type !== 'diff') return null;
      return (
        <InlineComposer
          draft={draft}
          fileDiff={item.fileDiff}
          versionId={session.versionId}
          onClose={closeDraft}
        />
      );
    },
    [draft, session.versionId, closeDraft],
  );

  useImperativeHandle(
    ref,
    () => ({
      scrollToFile(path: string) {
        codeView.current?.scrollTo({ type: 'item', id: path, align: 'start', behavior: 'smooth' });
      },
      scrollToComment(comment: Comment) {
        codeView.current?.scrollTo({
          type: 'range',
          id: comment.filePath,
          range: {
            start: comment.range.start,
            end: comment.range.end,
            ...(comment.range.startSide ? { side: comment.range.startSide } : {}),
            ...(comment.range.endSide ? { endSide: comment.range.endSide } : {}),
          },
          align: 'center',
          behavior: 'smooth',
        });
      },
    }),
    [],
  );

  return (
    <div className="diff">
      <CodeView<AnnotationMeta>
        ref={codeView}
        className="codeview"
        items={items}
        options={options}
        renderAnnotation={renderAnnotation}
      />
    </div>
  );
}
