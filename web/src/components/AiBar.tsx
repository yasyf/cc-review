import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { KeyboardEvent, RefObject } from 'react';
import { aiStatus, isActive, recentUserCommands, resultStream } from '../lib/ai-requests';
import { useCreateAiRequest, useSetFileStates } from '../lib/api';
import type { FileStatePatch } from '../lib/api';
import { fileItemId } from '../lib/diff/items';
import type { FileRef } from '../lib/diff/items';
import { useEventStream } from '../lib/events';
import { matchFiles } from '../lib/glob';
import { useLocalRequests } from '../lib/local-requests';
import type { LocalRequest } from '../lib/local-requests';
import { useReview } from '../lib/review-context';
import { deriveSuggestions } from '../lib/suggestions';
import type { Suggestion } from '../lib/suggestions';
import type { SessionResponse } from '../lib/types';
import { AiResultCard, LocalResultCard } from './AiResultCard';
import { CommandMenu } from './CommandMenu';
import type { MenuRow } from './CommandMenu';
import type { DiffViewHandle } from '../lib/diff/useDiffHandle';
import { Icon } from './ui/Icon';
import { Tooltip } from './ui/Tooltip';

const REORGANIZE_PROMPT = 'Re-organize this review into chapters and rate per-file risk.';

export function AiBar({
  session,
  diffRef,
}: {
  session: SessionResponse;
  diffRef: RefObject<DiffViewHandle | null>;
}) {
  const { slug } = useReview();
  const createRequest = useCreateAiRequest(slug);
  const setFileStates = useSetFileStates(slug, session.version);
  const local = useLocalRequests();
  const { peerPresent } = useEventStream();
  const [query, setQuery] = useState('');
  const [menuOpen, setMenuOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [streamOpen, setStreamOpen] = useState(false);
  const rootRef = useRef<HTMLElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const connected = peerPresent ?? false;

  // ⌘K opens and focuses the deck from anywhere; Esc (handled on the input) closes.
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setMenuOpen(true);
        inputRef.current?.focus();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // A click outside the deck dismisses the menu.
  useEffect(() => {
    if (!menuOpen && !streamOpen) return;
    function onDown(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
        setStreamOpen(false);
      }
    }
    window.addEventListener('mousedown', onDown);
    return () => window.removeEventListener('mousedown', onDown);
  }, [menuOpen, streamOpen]);

  const suggestions = useMemo(() => deriveSuggestions(session), [session]);
  const recents = useMemo(() => recentUserCommands(session.aiRequests), [session.aiRequests]);
  const allRefs = useMemo<FileRef[]>(
    () =>
      session.sections.flatMap((s) => s.files.map((f) => ({ sectionKey: s.sectionKey, path: f.path }))),
    [session.sections],
  );
  const sectionByKey = useMemo(
    () => new Map(session.sections.map((s) => [s.sectionKey, s])),
    [session.sections],
  );

  const runInstant = useCallback(
    (label: string, patches: FileStatePatch[]) => {
      if (patches.length === 0) return;
      const prior: Record<string, { reviewed: boolean; hidden: boolean }> = {};
      const refs: FileRef[] = [];
      for (const p of patches) {
        prior[fileItemId(p.sectionKey, p.path)] = sectionByKey.get(p.sectionKey)?.fileStates[p.path] ?? {
          reviewed: false,
          hidden: false,
        };
        refs.push({ sectionKey: p.sectionKey, path: p.path });
      }
      local.add(label, refs, prior);
      setFileStates.mutate(patches);
      setMenuOpen(false);
      setStreamOpen(true);
    },
    [local, sectionByKey, setFileStates],
  );

  const undoLocal = useCallback(
    (req: LocalRequest) => {
      setFileStates.mutate(
        req.refs.map((ref) => {
          const prior = req.prior[fileItemId(ref.sectionKey, ref.path)];
          return { sectionKey: ref.sectionKey, path: ref.path, reviewed: prior.reviewed, hidden: prior.hidden };
        }),
      );
      local.remove(req.id);
    },
    [local, setFileStates],
  );

  const sendAgent = useCallback(
    (prompt: string) => {
      const text = prompt.trim();
      if (!text || !connected) return;
      createRequest.mutate(text);
      setQuery('');
      setMenuOpen(false);
      setStreamOpen(true);
    },
    [connected, createRequest],
  );

  const reveal = useCallback(
    (ref: FileRef) => {
      diffRef.current?.scrollToFile(ref);
      setMenuOpen(false);
    },
    [diffRef],
  );

  const runSuggestion = useCallback(
    (s: Suggestion) => {
      switch (s.action.kind) {
        case 'hide':
          runInstant(s.label, s.action.refs.map((ref) => ({ ...ref, hidden: true })));
          break;
        case 'review':
          runInstant(s.label, s.action.refs.map((ref) => ({ ...ref, reviewed: true })));
          break;
        case 'reveal':
          reveal(s.action.ref);
          break;
      }
    },
    [runInstant, reveal],
  );

  const hidePattern = useCallback(
    (pattern: string) => {
      const refs = matchFiles(allRefs, pattern);
      runInstant(`Hid ${refs.length} matching ${pattern}`, refs.map((ref) => ({ ...ref, hidden: true })));
    },
    [allRefs, runInstant],
  );

  // The flat, ordered row list backing both the menu render and ↑↓/⏎ nav.
  const rows = useMemo<MenuRow[]>(() => {
    const out: MenuRow[] = [];
    const q = query.trim();
    const matches = q ? matchFiles(allRefs, q) : [];
    if (q && matches.length > 0) {
      out.push({
        id: 'tgt-hide',
        group: 'Target',
        lane: 'instant',
        label: `Hide ${matches.length} matching “${q}”`,
        run: () => runInstant(`Hid ${matches.length} matching ${q}`, matches.map((ref) => ({ ...ref, hidden: true }))),
      });
      out.push({
        id: 'tgt-view',
        group: 'Target',
        lane: 'instant',
        label: `Mark ${matches.length} matching viewed`,
        run: () => runInstant(`Marked ${matches.length} matching viewed`, matches.map((ref) => ({ ...ref, reviewed: true }))),
      });
    }
    for (const s of suggestions) {
      out.push({ id: `sug-${s.id}`, group: 'Suggested', lane: 'instant', label: s.label, run: () => runSuggestion(s) });
    }
    if (q) {
      out.push({ id: 'ask', group: 'Commands', lane: 'agent', label: `Ask Claude: “${q}”`, run: () => sendAgent(q) });
    }
    out.push({ id: 'reorg', group: 'Commands', lane: 'agent', label: 'Re-organize into chapters', run: () => sendAgent(REORGANIZE_PROMPT) });
    recents.forEach((prompt, i) => {
      out.push({ id: `rec-${i}`, group: 'Recent', lane: 'agent', label: prompt, editText: prompt, run: () => sendAgent(prompt) });
    });
    return out;
  }, [query, allRefs, suggestions, recents, runInstant, runSuggestion, sendAgent]);

  useEffect(() => {
    setActiveIndex((i) => (rows.length === 0 ? 0 : Math.min(i, rows.length - 1)));
  }, [rows.length]);

  const askingIds = session.aiRequests
    .filter((r) => r.status === 'awaiting_input')
    .map((r) => r.id)
    .join(',');
  useEffect(() => {
    if (askingIds) setStreamOpen(true);
  }, [askingIds]);

  if (session.review.status !== 'open') return null;

  function onComposerKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Escape') {
      setMenuOpen(false);
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setMenuOpen(true);
      setActiveIndex((i) => Math.min(i + 1, rows.length - 1));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex((i) => Math.max(i - 1, 0));
      return;
    }
    if (e.key === 'ArrowRight') {
      const row = rows[activeIndex];
      if (menuOpen && row?.editText !== undefined) {
        e.preventDefault();
        setQuery(row.editText);
      }
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const row = rows[activeIndex];
      if (menuOpen && row) {
        if (row.lane === 'agent' && !connected) return;
        row.run();
      } else {
        sendAgent(query);
      }
    }
  }

  const stream = resultStream(session.aiRequests, local.requests);
  const active = stream.filter((it) => it.kind === 'ai' && isActive(it.request));
  const rest = stream.filter((it) => !(it.kind === 'ai' && isActive(it.request)));
  const shownRest = historyOpen ? rest : rest.slice(0, 3);
  const status = aiStatus(session.aiRequests, connected);

  const renderItem = (it: (typeof stream)[number]) =>
    it.kind === 'ai' ? (
      <AiResultCard key={it.request.id} request={it.request} diffRef={diffRef} onHideMatching={hidePattern} />
    ) : (
      <LocalResultCard key={it.request.id} request={it.request} onUndo={() => undoLocal(it.request)} />
    );

  return (
    <footer className="ai-bar" ref={rootRef}>
      {streamOpen && !menuOpen ? (
        <div className="ai-pop" role="dialog" aria-label="Claude activity">
          <div className="ai-pop-head">
            <span>Claude activity</span>
            <button type="button" className="ai-mini" onClick={() => setStreamOpen(false)} aria-label="Close">
              <Icon name="x" size={12} />
            </button>
          </div>
          {stream.length === 0 ? <div className="ai-pop-empty">Nothing yet. Ask Claude below or press ⌘K.</div> : null}
          {active.map(renderItem)}
          {shownRest.map(renderItem)}
          {rest.length > 3 ? (
            <button type="button" className="ai-mini ai-pop-more" onClick={() => setHistoryOpen(!historyOpen)}>
              {historyOpen ? 'Show fewer' : `${rest.length - 3} more`}
            </button>
          ) : null}
        </div>
      ) : null}

      {menuOpen ? (
        <CommandMenu rows={rows} activeIndex={activeIndex} connected={connected} onHover={setActiveIndex} />
      ) : null}

      <div className="ai-row">
        <Tooltip
          label={
            connected
              ? 'Show Claude activity'
              : 'Run /cc-review:start in Claude Code to enable Claude actions; instant actions still work'
          }
          describe={false}
        >
          <button
            type="button"
            className={`ai-chip ai-chip-${status.tone}`}
            aria-expanded={streamOpen}
            onClick={() => {
              setMenuOpen(false);
              setStreamOpen(!streamOpen);
            }}
          >
            <span className="ai-chip-dot" aria-hidden="true" />
            {status.label}
            {status.detail ? <span className="ai-chip-detail">· {status.detail}</span> : null}
          </button>
        </Tooltip>
        <div className="ai-input">
          <Icon name="sparkle" size={14} className="ai-input-icon" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            placeholder={connected ? 'Ask Claude or run a command…' : 'Run a command…'}
            onFocus={() => setMenuOpen(true)}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onComposerKey}
          />
          <kbd className="ai-input-kbd">⌘K</kbd>
        </div>
      </div>
    </footer>
  );
}
