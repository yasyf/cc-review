import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { KeyboardEvent, RefObject } from 'react';
import { recentUserCommands, isActive, resultStream } from '../lib/ai-requests';
import { useCreateAiRequest, useSetFileStates } from '../lib/api';
import type { FileStatePatch } from '../lib/api';
import { fileItemId } from '../lib/diff';
import type { FileRef } from '../lib/diff';
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
import type { DiffViewHandle } from './DiffView';

const REORGANIZE_PROMPT = 'Re-organize this review into chapters and rate per-file risk.';

// The Command Deck: a resident footer that reads this diff and offers ranked
// one-tap chips in two lanes — ⚡ instant (client-side file-state ops, work
// offline) and ✦ Claude (semantic asks). ⌘K / focusing the composer opens an
// anchored menu upward. Hidden once the review is submitted.
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
    if (!menuOpen) return;
    function onDown(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setMenuOpen(false);
    }
    window.addEventListener('mousedown', onDown);
    return () => window.removeEventListener('mousedown', onDown);
  }, [menuOpen]);

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
  const shownRest = historyOpen ? rest : rest.slice(0, 1);

  const renderItem = (it: (typeof stream)[number]) =>
    it.kind === 'ai' ? (
      <AiResultCard key={it.request.id} request={it.request} diffRef={diffRef} onHideMatching={hidePattern} />
    ) : (
      <LocalResultCard key={it.request.id} request={it.request} onUndo={() => undoLocal(it.request)} />
    );

  return (
    <footer className="ai-bar deck" ref={rootRef}>
      {active.length > 0 || shownRest.length > 0 ? (
        <div className="deck-stream">
          {active.map(renderItem)}
          {shownRest.map(renderItem)}
          {rest.length > 1 ? (
            <button type="button" className="ai-mini deck-history-toggle" onClick={() => setHistoryOpen(!historyOpen)}>
              {historyOpen ? 'Hide history' : `${rest.length - 1} more`}
            </button>
          ) : null}
        </div>
      ) : null}

      {menuOpen ? (
        <CommandMenu rows={rows} activeIndex={activeIndex} connected={connected} onHover={setActiveIndex} />
      ) : null}

      <div className="deck-row">
        <div className="deck-chips">
          {suggestions.length === 0 ? (
            <span className="deck-empty">No quick actions — ask Claude below.</span>
          ) : (
            suggestions.slice(0, 3).map((s) => (
              <button key={s.id} type="button" className="deck-chip" onClick={() => runSuggestion(s)}>
                <span className="deck-bolt">⚡</span> {s.label}
              </button>
            ))
          )}
        </div>
        <div className="deck-input">
          <input
            ref={inputRef}
            type="text"
            value={query}
            placeholder="Ask Claude…   ⌘K"
            onFocus={() => setMenuOpen(true)}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onComposerKey}
          />
          <span
            className={`deck-presence${connected ? ' deck-presence-on' : ''}`}
            title={connected ? 'Claude connected' : 'Claude not connected'}
          />
        </div>
      </div>

      {!connected ? (
        <div className="ai-hint">⚡ actions work offline · run /cc-review:start to enable ✦ Claude actions.</div>
      ) : null}
    </footer>
  );
}
