import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { Ref } from 'react';
import type { CodeViewOptions, LineTypes, PostRenderPhase, SelectedLineRange } from '@pierre/diffs';
import { CodeView } from '@pierre/diffs/react';
import type { CodeViewHandle } from '@pierre/diffs/react';
import { useSetFileStates } from '../lib/api';
import {
  TURN_UNSAFE_CSS,
  buildTurnIndex,
  decorateContainer,
  firstOccurrence,
  turnIdAt,
} from '../lib/attribution';
import type { TurnIndexEntry } from '../lib/attribution';
import {
  IMPORTANCE_UNSAFE_CSS,
  buildImportanceIndex,
  decorateImportance,
  noteAt,
} from '../lib/importance';
import { ANNOTATION_UNSAFE_CSS, annotationsByFile, decorateAnnotations } from '../lib/annotations';
import { buildItems, parseFiles } from '../lib/diff';
import type { AnnotationMeta, ComposerDraft, ReviewItem } from '../lib/diff';
import { clearDraft, composerDraftKey } from '../lib/drafts';
import { fileOrder } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { Comment, LineLevel, SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { themes } from '../worker';
import { CommentThread } from './CommentThread';
import { FileHeaderControls } from './FileHeaderControls';
import { FocusPopover } from './FocusPopover';
import { InlineComposer } from './InlineComposer';
import { TurnPopover } from './TurnPopover';

// The imperative surface the rest of the app uses to navigate the diff; only
// this component talks to @pierre/diffs directly.
export interface DiffViewHandle {
  scrollToFile(path: string): void;
  scrollToComment(comment: Comment): void;
  focusNextFile(): void;
  focusPrevFile(): void;
  toggleViewedCurrent(): void;
  toggleCollapseCurrent(): void;
  focusNextComment(): void;
  focusPrevComment(): void;
}

type CodeViewInstance = NonNullable<ReturnType<CodeViewHandle<AnnotationMeta>['getInstance']>>;

// The current file switches when the next file's top crosses the viewport top
// (under the sticky header); +1px keeps the boundary inclusive.
const CURRENT_FILE_OFFSET_PX = 1;

type PendingScroll = { kind: 'file'; path: string } | { kind: 'comment'; comment: Comment };

type Hover =
  | { kind: 'turn'; entry: TurnIndexEntry; x: number; y: number }
  | { kind: 'focus'; note: string; level: LineLevel; x: number; y: number };

// A pending scroll whose target never re-enters `items` (e.g. its reveal
// mutation failed) must not fire a surprise scrollTo minutes later; drop it
// after this many items changes without the target appearing.
const MAX_PENDING_SCROLL_MISSES = 5;

// Index of the first ordered comment below the current scroll, file-granular
// (only file tops are exposed, not per-line offsets). `n` targets it, `p` the
// one before.
function commentIndexNearScroll(viewer: CodeViewInstance, comments: readonly Comment[]): number {
  const scrollTop = viewer.getScrollTop();
  let index = 0;
  for (let i = 0; i < comments.length; i++) {
    const top = viewer.getTopForItem(comments[i].filePath);
    if (top !== undefined && top <= scrollTop + CURRENT_FILE_OFFSET_PX) index = i + 1;
  }
  return index;
}

export function DiffView({ session, ref }: { session: SessionResponse; ref?: Ref<DiffViewHandle> }) {
  const { slug, version } = useReview();
  const {
    viewMode,
    hideReviewed,
    focusMode,
    expandOverrides,
    toggleExpandOverride,
    clearExpandOverride,
    activeTurnId,
  } = useViewPrefs();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const codeView = useRef<CodeViewHandle<AnnotationMeta>>(null);
  const seqRef = useRef(0);
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const [pendingScroll, setPendingScroll] = useState<PendingScroll | null>(null);
  const pendingScrollMisses = useRef(0);
  // One hover popover, two sources: a focus note wins over turn attribution on
  // the same addition row (focus owns opacity when no turn is selected).
  const [hover, setHover] = useState<Hover | null>(null);

  const readOnly = session.review.status !== 'open';

  const files = useMemo(() => parseFiles(session.patchText), [session.patchText]);
  const order = useMemo(() => fileOrder(session, viewMode), [session, viewMode]);
  // Generated/vendored files fold by default (peekable), keyed off session.files
  // so a Viewed toggle never rethrashes the set.
  const autoCollapse = useMemo(
    () => new Set(session.files.filter((f) => f.generated || f.vendored).map((f) => f.path)),
    [session.files],
  );
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
        autoCollapse,
      ),
    [
      files,
      session.comments,
      draft,
      session.fileStates,
      order,
      hideReviewed,
      expandOverrides,
      autoCollapse,
    ],
  );

  // Comments in display order (file rank, then anchor line) — the cursor `n`/`p`
  // walk. Never derived from getRenderedItems(): off-screen comments must count.
  const orderedComments = useMemo(
    () =>
      [...session.comments].sort((a, b) => {
        const ra = order.get(a.filePath) ?? Infinity;
        const rb = order.get(b.filePath) ?? Infinity;
        if (ra !== rb) return ra - rb;
        return a.range.end - b.range.end;
      }),
    [session.comments, order],
  );

  // Current-file tracking is DOM-only (a `.file-current` class); routing it
  // through items/version would re-collapse on every scroll.
  const currentPathRef = useRef<string | null>(null);
  const itemsRef = useRef(items);
  const orderedCommentsRef = useRef(orderedComments);
  const fileStatesRef = useRef(session.fileStates);

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

  const turnIndex = useMemo(() => buildTurnIndex(session.turns), [session.turns]);
  const importanceIndex = useMemo(
    () => buildImportanceIndex(session.organization),
    [session.organization],
  );
  const annotationsForFile = useMemo(
    () => annotationsByFile(session.annotations),
    [session.annotations],
  );

  // Attribution inputs ride through refs so the options identity stays stable
  // across turn-focus and attribution changes — an options swap would re-render
  // every diff. The decorate effect below repaints what's already on screen;
  // onPostRender covers everything rendered afterwards.
  const attributionsRef = useRef(session.attributions);
  const turnIndexRef = useRef(turnIndex);
  const activeTurnIdRef = useRef(activeTurnId);
  const annotationsRef = useRef(annotationsForFile);
  const importanceIndexRef = useRef(importanceIndex);
  const focusModeRef = useRef(focusMode);
  useEffect(() => {
    attributionsRef.current = session.attributions;
    turnIndexRef.current = turnIndex;
    activeTurnIdRef.current = activeTurnId;
    annotationsRef.current = annotationsForFile;
    importanceIndexRef.current = importanceIndex;
    focusModeRef.current = focusMode;
    itemsRef.current = items;
    orderedCommentsRef.current = orderedComments;
    fileStatesRef.current = session.fileStates;
  });

  useEffect(() => {
    const instance = codeView.current?.getInstance();
    if (!instance) return;
    for (const rendered of instance.getRenderedItems()) {
      decorateContainer(
        rendered.element,
        session.attributions[rendered.id] ?? [],
        turnIndex,
        activeTurnId,
      );
      decorateImportance(
        rendered.element,
        importanceIndex.get(rendered.id) ?? null,
        focusMode,
        activeTurnId,
      );
      decorateAnnotations(rendered.element, annotationsForFile[rendered.id] ?? []);
    }
  }, [session.attributions, turnIndex, activeTurnId, importanceIndex, focusMode, annotationsForFile]);

  // Focusing a turn (from the legend) jumps to its first attributed line; a
  // re-fire on attribution updates alone must not re-scroll.
  const prevActiveTurnId = useRef<string | null>(null);
  useEffect(() => {
    if (activeTurnId !== null && activeTurnId !== prevActiveTurnId.current) {
      const target = firstOccurrence(session.attributions, activeTurnId);
      if (target) {
        codeView.current?.scrollTo({
          type: 'range',
          id: target.file,
          range: { start: target.line, end: target.line, side: 'additions', endSide: 'additions' },
          align: 'center',
          behavior: 'smooth',
        });
      }
    }
    prevActiveTurnId.current = activeTurnId;
  }, [activeTurnId, session.attributions]);

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
      unsafeCSS: TURN_UNSAFE_CSS + IMPORTANCE_UNSAFE_CSS + ANNOTATION_UNSAFE_CSS,
      onPostRender: (
        node: HTMLElement,
        _instance: unknown,
        phase: PostRenderPhase,
        context: { item: { id: string } },
      ) => {
        if (phase === 'unmount') return;
        decorateContainer(
          node,
          attributionsRef.current[context.item.id] ?? [],
          turnIndexRef.current,
          activeTurnIdRef.current,
        );
        decorateImportance(
          node,
          importanceIndexRef.current.get(context.item.id) ?? null,
          focusModeRef.current,
          activeTurnIdRef.current,
        );
        decorateAnnotations(node, annotationsRef.current[context.item.id] ?? []);
        node.classList.toggle('file-current', context.item.id === currentPathRef.current);
      },
      onLineEnter: (
        props: { lineNumber: number; lineElement: HTMLElement; lineType?: LineTypes },
        context: { item: { id: string } },
      ) => {
        if (props.lineType !== 'change-addition') {
          setHover(null);
          return;
        }
        const rect = props.lineElement.getBoundingClientRect();
        const x = rect.left + 8;
        const y = rect.bottom + 4;
        // A focus note (when no turn is selected) owns the popover, mirroring
        // how decorateImportance owns opacity on the same row.
        const notes = importanceIndexRef.current.get(context.item.id);
        const note =
          focusModeRef.current && activeTurnIdRef.current === null && notes
            ? noteAt(notes, props.lineNumber)
            : undefined;
        if (note) {
          setHover({ kind: 'focus', note: note.note, level: note.level, x, y });
          return;
        }
        const turnId = turnIdAt(attributionsRef.current[context.item.id] ?? [], props.lineNumber);
        const entry = turnId ? turnIndexRef.current.get(turnId) : undefined;
        if (!entry) {
          setHover(null);
          return;
        }
        setHover({ kind: 'turn', entry, x, y });
      },
      onLineLeave: () => setHover(null),
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
      if ((state?.reviewed || autoCollapse.has(path)) && !expandOverrides.has(path)) {
        toggleExpandOverride(path);
      }
    },
    [session.fileStates, expandOverrides, toggleExpandOverride, mutateStates, autoCollapse],
  );

  // Repaint the `.file-current` class on what's mounted now; onPostRender covers
  // rows rendered afterwards (recycled or scrolled into view).
  const applyCurrentPath = useCallback((path: string | null) => {
    if (currentPathRef.current === path) return;
    currentPathRef.current = path;
    const viewer = codeView.current?.getInstance();
    if (!viewer) return;
    for (const rendered of viewer.getRenderedItems()) {
      rendered.element.classList.toggle('file-current', rendered.id === path);
    }
  }, []);

  // Topmost file whose measured top sits at/above the viewport top — the file
  // the sticky header currently represents. Walks the full in-memory order.
  const syncCurrentFromScroll = useCallback(
    (viewer: CodeViewInstance) => {
      const scrollTop = viewer.getScrollTop();
      const list = itemsRef.current;
      let currentId: string | null = list.length > 0 ? list[0].id : null;
      for (const item of list) {
        const top = viewer.getTopForItem(item.id);
        if (top === undefined) continue;
        if (top <= scrollTop + CURRENT_FILE_OFFSET_PX) currentId = item.id;
        else break;
      }
      applyCurrentPath(currentId);
    },
    [applyCurrentPath],
  );

  // j/k glide: scroll to a file without peeking it open (no reveal()).
  const goToFile = useCallback(
    (path: string) => {
      codeView.current?.scrollTo({ type: 'item', id: path, align: 'start', behavior: 'smooth' });
      applyCurrentPath(path);
    },
    [applyCurrentPath],
  );

  const scrollToCommentImpl = useCallback(
    (comment: Comment) => {
      reveal(comment.filePath);
      pendingScrollMisses.current = 0;
      setPendingScroll({ kind: 'comment', comment });
    },
    [reveal],
  );

  useEffect(() => {
    const viewer = codeView.current?.getInstance();
    if (viewer) syncCurrentFromScroll(viewer);
  }, [items, syncCurrentFromScroll]);

  useImperativeHandle(
    ref,
    () => ({
      scrollToFile(path: string) {
        reveal(path);
        pendingScrollMisses.current = 0;
        setPendingScroll({ kind: 'file', path });
      },
      scrollToComment(comment: Comment) {
        scrollToCommentImpl(comment);
      },
      focusNextFile() {
        const list = itemsRef.current;
        if (list.length === 0) return;
        const idx = list.findIndex((item) => item.id === currentPathRef.current);
        goToFile(list[idx < 0 ? 0 : Math.min(idx + 1, list.length - 1)].id);
      },
      focusPrevFile() {
        const list = itemsRef.current;
        if (list.length === 0) return;
        const idx = list.findIndex((item) => item.id === currentPathRef.current);
        goToFile(list[idx < 0 ? 0 : Math.max(idx - 1, 0)].id);
      },
      toggleViewedCurrent() {
        const path = currentPathRef.current;
        if (!path) return;
        const reviewed = fileStatesRef.current[path]?.reviewed ?? false;
        if (reviewed) {
          mutateStates([{ path, reviewed: false }]);
          return;
        }
        // Capture the next file before mutating: with hideReviewed on, the just-
        // viewed file leaves `items` and the indices shift under us.
        const list = itemsRef.current;
        const nextId = list[list.findIndex((item) => item.id === path) + 1]?.id ?? null;
        clearExpandOverride(path);
        mutateStates([{ path, reviewed: true }]);
        if (nextId) goToFile(nextId);
      },
      toggleCollapseCurrent() {
        const path = currentPathRef.current;
        if (path) toggleExpandOverride(path);
      },
      focusNextComment() {
        const comments = orderedCommentsRef.current;
        const viewer = codeView.current?.getInstance();
        if (comments.length === 0 || !viewer) return;
        const i = commentIndexNearScroll(viewer, comments);
        scrollToCommentImpl(comments[Math.min(i, comments.length - 1)]);
      },
      focusPrevComment() {
        const comments = orderedCommentsRef.current;
        const viewer = codeView.current?.getInstance();
        if (comments.length === 0 || !viewer) return;
        const i = commentIndexNearScroll(viewer, comments);
        scrollToCommentImpl(comments[Math.max(i - 1, 0)]);
      },
    }),
    [reveal, scrollToCommentImpl, goToFile, mutateStates, toggleExpandOverride, clearExpandOverride],
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
        onScroll={(_, viewer) => syncCurrentFromScroll(viewer)}
        renderAnnotation={renderAnnotation}
        renderHeaderMetadata={renderHeaderMetadata}
      />
      {hover?.kind === 'turn' ? <TurnPopover entry={hover.entry} x={hover.x} y={hover.y} /> : null}
      {hover?.kind === 'focus' ? (
        <FocusPopover note={hover.note} level={hover.level} x={hover.x} y={hover.y} />
      ) : null}
    </div>
  );
}
