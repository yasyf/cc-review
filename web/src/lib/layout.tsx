import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { ReactNode } from 'react';

export type MainTab = 'overview' | 'files' | 'checks' | 'activity';

export type RailMode = 'docked' | 'hidden' | 'overlay';

export const FILES_MIN_WIDTH_PX = 200;
export const FILES_MAX_WIDTH_PX = 520;
export const FILES_DEFAULT_WIDTH_PX = 300;

const FILES_WIDTH_KEY = 'cc-review:files-width';
const FILES_CLOSED_KEY = 'cc-review:files-closed';
const RAIL_COLLAPSED_KEY = 'cc-review:rail-collapsed';
const NARROW_QUERY = '(max-width: 900px)';

export function parseMainTab(raw: unknown): MainTab | undefined {
  return raw === 'overview' || raw === 'checks' || raw === 'activity' ? raw : undefined;
}

export function clampFilesWidth(width: number): number {
  return Math.round(Math.min(FILES_MAX_WIDTH_PX, Math.max(FILES_MIN_WIDTH_PX, width)));
}

function readFilesWidth(): number {
  const stored = Number(localStorage.getItem(FILES_WIDTH_KEY));
  return stored > 0 ? clampFilesWidth(stored) : FILES_DEFAULT_WIDTH_PX;
}

function subscribeNarrow(onChange: () => void): () => void {
  const query = window.matchMedia(NARROW_QUERY);
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

function isNarrow(): boolean {
  return window.matchMedia(NARROW_QUERY).matches;
}

interface Layout {
  railMode: RailMode;
  toggleRail(): void;
  dismissRail(): void;
  filesOpen: boolean;
  toggleFiles(): void;
  filesWidth: number;
  setFilesWidth(width: number): void;
}

const LayoutContext = createContext<Layout | null>(null);

export function useLayout(): Layout {
  const value = useContext(LayoutContext);
  if (!value) throw new Error('useLayout must be used within LayoutFrame');
  return value;
}

function usePersistedFlag(key: string): [boolean, () => void] {
  const [flag, setFlag] = useState(() => localStorage.getItem(key) === 'true');
  const toggle = useCallback(() => {
    setFlag((prev) => {
      localStorage.setItem(key, String(!prev));
      return !prev;
    });
  }, [key]);
  return [flag, toggle];
}

export function LayoutFrame({ hasRail, children }: { hasRail: boolean; children: ReactNode }) {
  const narrow = useSyncExternalStore(subscribeNarrow, isNarrow);
  const [railCollapsed, toggleRailCollapsed] = usePersistedFlag(RAIL_COLLAPSED_KEY);
  const [filesClosed, toggleFiles] = usePersistedFlag(FILES_CLOSED_KEY);
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [filesWidth, setFilesWidthState] = useState(readFilesWidth);
  const ref = useRef<HTMLDivElement>(null);

  const railMode: RailMode = !hasRail
    ? 'hidden'
    : narrow
      ? overlayOpen
        ? 'overlay'
        : 'hidden'
      : railCollapsed
        ? 'hidden'
        : 'docked';

  useLayoutEffect(() => {
    ref.current?.style.setProperty('--files-w', `${filesWidth}px`);
  }, [filesWidth]);

  useEffect(() => {
    if (!narrow) setOverlayOpen(false);
  }, [narrow]);

  useEffect(() => {
    if (!overlayOpen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOverlayOpen(false);
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [overlayOpen]);

  const setFilesWidth = useCallback((next: number) => {
    const clamped = clampFilesWidth(next);
    localStorage.setItem(FILES_WIDTH_KEY, String(clamped));
    setFilesWidthState(clamped);
  }, []);

  const toggleRail = useCallback(() => {
    if (!hasRail) return;
    if (narrow) setOverlayOpen((open) => !open);
    else toggleRailCollapsed();
  }, [hasRail, narrow, toggleRailCollapsed]);

  const dismissRail = useCallback(() => setOverlayOpen(false), []);

  return (
    <LayoutContext.Provider
      value={{ railMode, toggleRail, dismissRail, filesOpen: !filesClosed, toggleFiles, filesWidth, setFilesWidth }}
    >
      <div ref={ref} className="review-root" data-rail={railMode} data-files={filesClosed ? 'closed' : 'open'}>
        {children}
        {railMode === 'overlay' ? <div className="rail-scrim" onClick={dismissRail} /> : null}
      </div>
    </LayoutContext.Provider>
  );
}
