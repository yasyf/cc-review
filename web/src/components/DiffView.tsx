import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { Ref } from 'react';
import type { CodeViewOptions, SelectedLineRange } from '@pierre/diffs';
import { CodeView } from '@pierre/diffs/react';
import type { CodeViewHandle } from '@pierre/diffs/react';
import { useSetFileStates } from '../lib/api';
import { buildItems, parseFiles } from '../lib/diff';
import type { AnnotationMeta, ComposerDraft, ReviewItem } from '../lib/diff';
import { clearDraft, composerDraftKey } from '../lib/drafts';
import { fileOrder } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { Comment, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { themes } from '../worker';
import { CommentThread } from './CommentThread';
import { FileHeaderControls } from './FileHeaderControls';
import { InlineComposer } from './InlineComposer';

// The imperative surface the rest of the app uses to navigate the diff; only
// this component talks to @pierre/diffs directly.
export interface DiffViewHandle {
  scrollToFile(path: string): void;
  scrollToComment(comment: Comment): void;
}

type PendingScroll = { kind: 'file'; path: string } | { kind: 'comment'; comment: Comment };

// A pending scroll whose target never re-enters `items` (e.g. its reveal
// mutation failed) must not fire a surprise scrollTo minutes later; drop it
// after this many items changes without the target appearing.
const MAX_PENDING_SCROLL_MISSES = 5;

export function DiffView({ session, ref }: { session: SessionResponse; ref?: Ref<DiffViewHandle> }) {
  const { slug, version } = useReview();
  const { viewMode, hideReviewed, expandOverrides, toggleExpandOverride } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const codeView = useRef<CodeViewHandle<AnnotationMeta>>(null);
  const seqRef = useRef(0);
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const [pendingScroll, setPendingScroll] = useState<PendingScroll | null>(null);
  const pendingScrollMisses = useRef(0);

  const readOnly = session.review.status === 'submitted';

  const files = useMemo(() => parseFiles(session.patchText), [session.patchText]);
  const order = useMemo(() => fileOrder(session, viewMode), [session, viewMode]);
  const items = useMemo(
    () =>
      buildItems(
        files,
        session.comments,
        draft,
        session.fileStates,
        order,
        hideReviewed,
        expandOverrides,
      ),
    [files, session.comments, draft, session.fileStates, order, hideReviewed, expandOverrides],
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

  // A hidden or filtered-out file can no longer host the composer.
  useEffect(() => {
    if (draft && !items.some((item) => item.id === draft.filePath)) closeDraft();
  }, [draft, items, closeDraft]);

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

  const renderHeaderMetadata = useCallback(
    (item: ReviewItem) => <FileHeaderControls path={item.id} />,
    [],
  );

  // Unhide/peek the target up front; the scroll itself waits in the effect
  // below until the revealed item is back in `items`.
  const reveal = useCallback(
    (path: string) => {
      const state = session.fileStates[path];
      // A failed unhide means the target never re-enters items; the scroll
      // must die with it.
      if (state?.hidden) {
        mutateStates([{ path, hidden: false }], { onError: () => setPendingScroll(null) });
      }
      if (state?.reviewed && !expandOverrides.has(path)) toggleExpandOverride(path);
    },
    [session.fileStates, expandOverrides, toggleExpandOverride, mutateStates],
  );

  useImperativeHandle(
    ref,
    () => ({
      scrollToFile(path: string) {
        reveal(path);
        pendingScrollMisses.current = 0;
        setPendingScroll({ kind: 'file', path });
      },
      scrollToComment(comment: Comment) {
        reveal(comment.filePath);
        pendingScrollMisses.current = 0;
        setPendingScroll({ kind: 'comment', comment });
      },
    }),
    [reveal],
  );

  useEffect(() => {
    if (!pendingScroll) return;
    const path = pendingScroll.kind === 'file' ? pendingScroll.path : pendingScroll.comment.filePath;
    if (!items.some((item) => item.id === path)) {
      if (++pendingScrollMisses.current >= MAX_PENDING_SCROLL_MISSES) setPendingScroll(null);
      return;
    }
    if (pendingScroll.kind === 'file') {
      codeView.current?.scrollTo({ type: 'item', id: path, align: 'start', behavior: 'smooth' });
    } else {
      const { comment } = pendingScroll;
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
    }
    setPendingScroll(null);
  }, [pendingScroll, items]);

  return (
    <div className="diff">
      <CodeView<AnnotationMeta>
        ref={codeView}
        className="codeview"
        items={items}
        options={options}
        renderAnnotation={renderAnnotation}
        renderHeaderMetadata={renderHeaderMetadata}
      />
    </div>
  );
}
